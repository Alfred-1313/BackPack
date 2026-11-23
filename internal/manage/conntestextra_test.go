package manage

import (
	"context"
	"encoding/base64"
	"io"
	"net"
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
		{Transport: "omni-icmp", Phase: ctPhaseTransportQueued, Status: ctTesting},
		{Transport: ctGostDirect, Phase: ctPhaseTransportQueued, Status: ctTesting},
	}
	if got, want := ConnTestTimeLeft(rows), ctSpeedSecondPass()+2*ctExtraTime(); got != want {
		t.Fatalf("time left %s, want %s", got, want)
	}
	for phase, want := range map[string]string{
		ctPhaseTransportQueued:  "AWAIT SETUP",
		ctPhaseTransportSetup:   "SETTING UP",
		ctPhaseTransportConnect: "CONNECTING",
		ctPhaseTransportEcho:    "ECHO TEST",
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
	rows[1].Phase, rows[1].Total, rows[1].Tried = ctPhaseTransportEcho, 60, 17
	if got, want := ConnTestTimeLeft(rows), 43*time.Second+ConnTestSpeedTime(1)+ctExtraTime(); got != want {
		t.Fatalf("time left during echoes %s, want %s", got, want)
	}
	rows[0].Status = ctTesting
	if got := ConnTestTimeLeft(rows); got != 0 {
		t.Fatalf("should keep the overall estimate while native stability is in progress, got %s", got)
	}
	rows[0].Phase, rows[1].Phase = "speed-queued", "speed"
	if got, want := ConnTestTimeLeft(rows), ConnTestSpeedTime(2)+ctExtraTime(); got != want {
		t.Fatalf("time left with native speed queued %s, want %s", got, want)
	}
}

func TestHelperStabilityStartsAfterMatchingPayload(t *testing.T) {
	for _, match := range []bool{true, false} {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				payload := make([]byte, 512)
				for {
					if _, err := io.ReadFull(conn, payload); err != nil {
						break
					}
					if !match {
						payload[0] ^= 1
					}
					if _, err := conn.Write(payload); err != nil {
						break
					}
				}
				conn.Close()
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		route := connTestCase{kind: "direct", extra: true, entry: l.Addr().(*net.TCPAddr).Port}
		result := ctProbe(ctx, &route, 0, func(row ConnTestResult) {
			if row.OK == 1 {
				cancel()
			}
		})
		cancel()
		l.Close()
		if got := result.OK > 0; got != match {
			t.Fatalf("matching payload %t, stability verified %d echoes", match, result.OK)
		}
	}
}

func TestConnectionTestNegotiatesParallelHelpers(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(make([]byte, ctPairKeySize))
	aead, err := ctExtraCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	control := &ctExtraControl{key: aead}
	for _, tc := range []struct {
		value    any
		parallel bool
	}{
		{true, false},
		{ctExtraParallel, true},
	} {
		msg, err := ctExtraSeal(aead, "ready", tc.value)
		if err != nil {
			t.Fatal(err)
		}
		if control.answer("extra-ready", msg) != "ok" || control.parallelReady() != tc.parallel {
			t.Fatalf("readiness %v did not negotiate the expected helper mode", tc.value)
		}
	}
}

func TestConnectionTestReleasesOnlyTheNamedHelper(t *testing.T) {
	a, otherA := net.Pipe()
	b, otherB := net.Pipe()
	t.Cleanup(func() {
		a.Close()
		b.Close()
		otherA.Close()
		otherB.Close()
	})
	first, second := &ctEchoes{}, &ctEchoes{}
	first.add(a)
	second.add(b)
	worker := &ctExtraWorker{parallel: true, instances: map[string]*ctExtraInstance{
		"first":  {echo: first},
		"second": {echo: second},
	}}
	if err := worker.close("first"); err != nil {
		t.Fatal(err)
	}
	if !first.closed || second.closed || worker.instances["first"] != nil || worker.instances["second"] == nil {
		t.Fatal("closing one helper affected another helper's echo listener")
	}
}
