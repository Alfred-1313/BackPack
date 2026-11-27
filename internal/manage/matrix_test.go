package manage

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/matrix"
)

func TestMatrixDirectionUsesTheSelectedListener(t *testing.T) {
	for _, tc := range []struct {
		form             MatrixForm
		direction        string
		iranID, kharejID int
	}{
		{MatrixForm{Direction: matrix.Direct}, matrix.Direct, 2, 1},
		{MatrixForm{Direction: matrix.Reverse}, matrix.Reverse, 1, 2},
		{MatrixForm{Main: "iran"}, matrix.Reverse, 1, 2},
		{MatrixForm{}, matrix.Direct, 2, 1},
	} {
		direction, err := tc.form.connectionDirection()
		iran, kharej := matrix.EndpointIDs(direction)
		if err != nil || direction != tc.direction || iran != tc.iranID || kharej != tc.kharejID {
			t.Fatalf("unexpected Matrix roles for %+v: %q %d/%d (%v)", tc.form, direction, iran, kharej, err)
		}
	}
	have := map[string]string{}
	for _, c := range ctExtraCases() {
		if matrix.IsDirectionalMode(c.tr) {
			have[c.tr] = c.kind
		}
	}
	if have[matrix.DirectID] != matrix.Direct || have[matrix.ReverseID] != matrix.Reverse {
		t.Fatalf("Matrix Connection Test directions: %v", have)
	}
	if _, err := (MatrixForm{Direction: matrix.Reverse, Main: "kharej"}).connectionDirection(); err == nil {
		t.Fatal("accepted a Matrix Main that conflicts with the selected direction")
	}
}

func TestMatrixSetupKeepsThePrivateKeyOnItsNode(t *testing.T) {
	priv, pub, err := matrix.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	block := netip.MustParsePrefix("10.203.5.0/24")
	main := matrixNode(block, 1, "127.0.0.1", pub, 2097)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	info, err := startPeerJoinProtocol(ctx, "127.0.0.1", "matrix-fixture", "direct", true,
		func(_ context.Context, _, addr, key string) (peerJoinResult, error) {
			node := matrixNode(block, 2, addr, key, 2097)
			far := config.MatrixConfig{Subnet: block.String(), Local: node, Peers: []config.MatrixHost{main}}
			return peerJoinResult{Driver: matrix.ID, Matrix: &far}, nil
		}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchPeerJoin(FindSetupLink(info.Apply))
	if err != nil {
		t.Fatal(err)
	}
	if err := matrix.Validate(*got.Matrix); err != nil {
		t.Fatal(err)
	}
	if got.Matrix.PrivateKey == priv || got.Matrix.Local.Key == pub || got.Matrix.Peers[0].Key != pub {
		t.Fatal("Matrix setup did not keep each node's private key local")
	}
	body, err := json.Marshal(got)
	if err != nil || strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatal("the Matrix enrolment response exposes a private key")
	}
}
