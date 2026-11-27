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
