package omni

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestEveryOmniTransportCanReachTheManualSetupCommand(t *testing.T) {
	for _, transport := range transports {
		t.Run(transport.value, func(t *testing.T) {
			op, err := parse([]string{"setup", transport.value, "example", "198.18.0.1", "198.18.0.2"}, strings.NewReader(""))
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"add-manual", transport.value, "example", "198.18.0.2", "16", "none", "198.18.0.1"}
			if !reflect.DeepEqual(op.args, want) {
				t.Fatalf("helper args = %q, want %q", op.args, want)
			}
		})
	}
}

func TestApplyTokenKeepsCredentialsOutOfProcessArguments(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("example setup material"))
	op, err := parse([]string{"join"}, strings.NewReader(token+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(op.args, []string{"server-token"}) || op.token != token {
		t.Fatal("setup token must reach the helper on its input")
	}
	if _, err := parse([]string{"join", token}, strings.NewReader("")); err == nil {
		t.Fatal("setup token in arguments was accepted")
	}
}

func TestInvalidTunnelSettingsCannotReachTheHelper(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "unknown", "test", "198.18.0.1", "198.18.0.2"},
		{"setup", "fou", "../test", "198.18.0.1", "198.18.0.2"},
		{"setup", "vxlan", "test", "198.18.0.1", "198.18.0.1"},
		{"setup", "ackless", "test", "::1", "198.18.0.2"},
		{"forward", "test", "tcp", "80:70000"},
		{"remove-forward", "test", "80", "--invalid"},
		{"remove", "*"},
		{"update"},
	} {
		if _, err := parse(args, strings.NewReader("")); err == nil {
			t.Errorf("unexpectedly accepted %q", args)
		}
	}
}

func TestRemoveForwardCanConfirmRemovingTheLastMapping(t *testing.T) {
	op, err := parse([]string{"remove-forward", "test", "80", "--force"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pf-del", "test", "80", "--force"}; !reflect.DeepEqual(op.args, want) {
		t.Fatalf("helper args = %q, want %q", op.args, want)
	}
}
