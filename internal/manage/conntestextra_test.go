package manage

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestPairedConnectionTestLinkKeepsKeyOutOfCoordinatorConfig(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(make([]byte, ctPairKeySize))
	link := ConnTestLink{Host: "192.0.2.1", Coord: 12345, Tok: ctNewSecret(), pairKey: key}
	addr, err := parseConnTestLink(link.Short())
	if err != nil || addr.PairKey != key || addr.Host != link.Host || addr.Tok != link.Tok {
		t.Fatalf("paired link read: %+v, %v", addr, err)
	}
	config, err := unGzipB64(ctConfig(link))
	if err != nil || strings.Contains(string(config), key) {
		t.Fatal("pairing key leaked through the test coordinator")
	}
	link.pairKey = ""
	addr, err = parseConnTestLink(link.Short())
	if err != nil || addr.PairKey != "" {
		t.Fatalf("legacy link read: %+v, %v", addr, err)
	}
}

func TestConnectionTestExtraJobAuthenticationAndChunking(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(make([]byte, ctPairKeySize))
	port := ctPickPort(map[int]bool{}, true)
	coordinator, err := startCTCoordinator(port, ctNewSecret())
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	aead, _ := ctExtraCipher(key)
	ctrl := &ctExtraControl{key: aead}
	coordinator.mu.Lock()
	coordinator.extra = ctrl
	coordinator.mu.Unlock()
	if got := coordinator.answer("extra-ready "+coordinator.tok+" invalid", "127.0.0.1"); got != "" {
		t.Fatalf("unauthenticated worker accepted: %q", got)
	}

	link := ConnTestLink{Host: "127.0.0.1", Coord: port, Tok: coordinator.tok, pairKey: key}
	token := strings.Repeat("large-private-material", 100)
	type response struct {
		r   ctExtraReply
		err error
	}
	result := make(chan response, 1)
	go func() {
		r, err := ctrl.exchange(ctx, ctExtraJob{Action: "join", Token: token})
		result <- response{r, err}
	}()
	var job ctExtraJob
	for ctx.Err() == nil {
		job, err = ctExtraReadJob(ctx, link, aead)
		if err != nil || job.ID != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || job.Token != token {
		t.Fatalf("fragmented job read: ID %d, %v", job.ID, err)
	}
	previous, _ := ctExtraSeal(aead, "reply", ctExtraReply{ID: job.ID - 1})
	if got := ctrl.answer("extra-reply", previous); got != "" {
		t.Fatal("reply for a different job was accepted")
	}
	sealed, _ := ctExtraSeal(aead, "reply", ctExtraReply{ID: job.ID, Port: 23456})
	got, err := ctAskContext(ctx, link.Host, link.Coord, "extra-reply "+link.Tok+" "+sealed)
	if err != nil || got != "ok" {
		t.Fatalf("reply acknowledgement: %q, %v", got, err)
	}
	if reply := <-result; reply.err != nil || reply.r.Port != 23456 {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}

func TestConnectionTestQueueKeepsHelpersInTheRemainingTime(t *testing.T) {
	rows := []ConnTestResult{
		{Transport: "tcp", Phase: ctPhaseSecond},
		{Transport: "omni-gre", Phase: ctPhaseTransportQueued, Status: ctTesting},
		{Transport: ctGostDirect, Phase: ctPhaseTransportQueued, Status: ctTesting},
	}
	if got, want := ConnTestTimeLeft(rows), ctSpeedSecondPass()+2*ctExtraTime(); got != want {
		t.Fatalf("time left %s, want %s", got, want)
	}
	for phase, want := range map[string]string{
		ctPhaseTransportQueued:  "QUEUED",
		ctPhaseTransportSetup:   "SETTING UP",
		ctPhaseTransportConnect: "CONNECTING",
		ctPhaseTransportCleanup: "CLEANING UP",
	} {
		if got := ctStatusLabel(ConnTestResult{Status: ctTesting, Phase: phase}); got != want {
			t.Errorf("phase %s: label %s, want %s", phase, got, want)
		}
	}
	rows[0].Phase = ""
	rows[1].Phase = ctPhaseTransportCleanup
	if got, want := ConnTestTimeLeft(rows), 40*time.Second+ctExtraTime(); got != want {
		t.Fatalf("time left during cleanup %s, want %s", got, want)
	}
}
