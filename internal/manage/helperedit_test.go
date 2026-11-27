package manage

import (
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/backpack/backpack/config"
)

const editingNativeDagger = "[backhaul]\nnative = true\nrole = \"server\"\nmode = \"dagger-https\"\niran_host = \"192.0.2.1\"\nport = 1824\ntoken = \"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\"\nports = [\"2083\", \"2087\"] # keep the forwards\nhttp_path = \"/test\"\n[extra]\nmarker = \"keep\"\n"

func TestHelperEditPatchesNativeDaggerSettings(t *testing.T) {
	patch := map[string]json.RawMessage{
		"port":      json.RawMessage("31111"),
		"ports":     json.RawMessage("\"2083=8443, 2087\""),
		"http_path": json.RawMessage("\"/next\""),
		"preset":    json.RawMessage("\"aggressive\""),
	}
	body, before, after, shared, err := prepareHelperEdit("edit-fixture", editingNativeDagger, patch, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "# keep the forwards") || !strings.Contains(body, "marker = \"keep\"") {
		t.Fatalf("lost comments or unknown settings: %s", body)
	}
	if before.Backhaul.Port != 1824 || after.Backhaul.Port != 31111 ||
		after.Backhaul.Lanes != 8 || after.Backhaul.HTTPPath != "/next" ||
		after.Backhaul.Token != before.Backhaul.Token || !reflect.DeepEqual(after.Backhaul.Ports, []string{"2083=8443", "2087"}) {
		t.Fatalf("incorrect changes: %+v", after.Backhaul)
	}
	if len(shared) != 3 || shared["ports"] != nil {
		t.Fatalf("unexpected updates for Kharej: %v", shared)
	}
	raw := strings.ReplaceAll(strings.ReplaceAll(editingNativeDagger, `role = "server"`, `role = "client"`), `ports = ["2083", "2087"] # keep the forwards`, "")
	_, originalPeer, updatedPeer, _, err := prepareHelperEdit("renamed", raw, shared, true)
	if err != nil {
		t.Fatal(err)
	}
	if updatedPeer.Backhaul.Role != "client" || updatedPeer.Backhaul.Port != 31111 ||
		updatedPeer.Backhaul.HTTPPath != "/next" || updatedPeer.Backhaul.Token != originalPeer.Backhaul.Token ||
		len(updatedPeer.Backhaul.Ports) != 0 {
		t.Fatalf("unexpected remote edits: %+v", updatedPeer.Backhaul)
	}
}

func TestHelperEditRejectsUnknownKeysAndDetectsNewPortConflicts(t *testing.T) {
	for _, key := range []string{"mode", "role", "token"} {
		if _, _, _, _, err := prepareHelperEdit("edit-fixture", editingNativeDagger,
			map[string]json.RawMessage{key: json.RawMessage("\"invalid\"")}, false); err == nil {
			t.Errorf("accepted %s", key)
		}
	}
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer port.Close()
	s := config.BackhaulConfig{Native: true, Role: "server", Mode: "dagger-https", Port: 1824}
	before := config.Config{Backhaul: s}
	after := before
	after.Backhaul.Ports = []string{port.Addr().String() + "=443"}
	if err := checkHelperEditPorts(before, after); err == nil {
		t.Fatal("accepted an occupied port")
	}
	if err := checkHelperEditPorts(after, after); err != nil {
		t.Fatalf("rejected tunnel's own unchanged port: %v", err)
	}
}
