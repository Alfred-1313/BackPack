package manage

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/tunnel/naive"
	"golang.org/x/net/proxy"
)

func TestAnyTLSThroughOfficialHelper(t *testing.T) {
	binary := os.Getenv("BP_TEST_SING_BOX")
	if binary == "" {
		t.Skip("set BP_TEST_SING_BOX to run the real AnyTLS helper")
	}
	fakeHTTPSHelpers(t)
	panelHelperPath = func(string) string { return binary }

	form := NewTunnel{Role: "server", Transport: "anytls", Name: "anytls-test", Host: "127.0.0.1",
		Token: "test-token-for-anytls-only", TunnelPort: freeHTTPSPort(t), Ports: freeHTTPSPort(t)}
	server, err := specFromNew(form)
	if err != nil {
		t.Fatal(err)
	}
	var far, near config.Config
	if _, err := toml.Decode(server.Render(), &near); err != nil {
		t.Fatal(err)
	}
	wire, err := shareLinkOf(server.Name, form.Host, near)
	if err != nil {
		t.Fatal(err)
	}
	link, err := DecodeShareLink(wire)
	if err != nil {
		t.Fatal(err)
	}
	client := reverseClientFromLink(link, form.Host)
	client.NaiveClient.Binary = binary
	client.NaiveClient.CAFile = filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(client.NaiveClient.CAFile, []byte(link.HelperCA), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(client.Render(), &far); err != nil {
		t.Fatal(err)
	}
	if near.Server.Naive.Mode != "anytls" || far.Client.Naive.Mode != "anytls" {
		t.Fatal("the peer setup lost the AnyTLS mode")
	}

	target, err := net.Listen("tcp", near.Server.BindAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		conn, err := target.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sh, err := naive.StartServer(ctx, &near.Server, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	ch, err := naive.StartClient(ctx, &far.Client, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	addr, err := url.Parse(ch.ProxyURL())
	if err != nil {
		t.Fatal(err)
	}
	dialer, err := proxy.FromURL(addr, &net.Dialer{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.Dial("tcp", near.Server.BindAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	const message = "anytls-data-round-trip"
	if _, err := io.WriteString(conn, message); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len(message))
	if _, err := io.ReadFull(conn, echo); err != nil || string(echo) != message {
		t.Fatalf("AnyTLS echo did not traverse the helpers: %v", err)
	}
}
