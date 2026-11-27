package manage

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/core"
)

const helperEditScheme = "backpack-edit://1."

type helperPeerEdit struct {
	Old     removalLink                `json:"old"`
	Next    string                     `json:"next"`
	Changes map[string]json.RawMessage `json:"changes"`
}

type HelperEditResult struct {
	PeerToken string
	Setup     *SetupLinkInfo
}

var helperEditMu sync.Mutex

func EditHelperTunnel(name string, edit HelperEdit) (HelperEditResult, error) {
	if err := core.CheckName(name); err != nil {
		return HelperEditResult{}, err
	}
	helperEditMu.Lock()
	defer helperEditMu.Unlock()
	raw, err := os.ReadFile(app.ConfigPath(name))
	if err != nil {
		return HelperEditResult{}, err
	}
	if edit.Revision != helperEditRevision(string(raw)) {
		return HelperEditResult{}, errors.New("the tunnel changed while it was open; reopen Edit and try again")
	}
	body, before, after, shared, err := prepareHelperEdit(name, string(raw), edit.Changes, false)
	if err != nil {
		return HelperEditResult{}, err
	}

	var result HelperEditResult
	if len(shared) > 0 {
		old, side := peerRemovalIdentity(Tunnel{Name: name}, before)
		if side == "iran" && old != "" {
			next, _ := peerRemovalIdentity(Tunnel{Name: name}, after)
			if next == "" {
				return HelperEditResult{}, errors.New("the new settings cannot identify this tunnel on Kharej")
			}
			data, err := json.Marshal(helperPeerEdit{Old: removalLink{Name: name, ID: old}, Next: next, Changes: shared})
			if err != nil {
				return HelperEditResult{}, err
			}
			result.PeerToken = helperEditScheme + base64.RawURLEncoding.EncodeToString(data)
			kind := "reverse"
			if before.Pengu.Direction == "direct" || before.Backhaul.Mode == "tun-ipx" || before.Dagger.Flavor == "tun" {
				kind = "direct"
			}
			result.Setup = &SetupLinkInfo{Name: name, Kind: kind, For: "kharej",
				Apply: "sudo backpack link update '" + result.PeerToken + "'"}
		}
	}
	if err := checkHelperEditPorts(before, after); err != nil {
		return HelperEditResult{}, err
	}
	return result, writeHelperEdit(name, string(raw), body)
}

func ApplyHelperPeerEdit(raw, name string) (string, error) {
	token, ok := strings.CutPrefix(strings.TrimSpace(raw), helperEditScheme)
	if !ok || len(token) > 16384 {
		return "", errors.New("expected an edit link from the Iran panel")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	var edit helperPeerEdit
	if err != nil || json.Unmarshal(data, &edit) != nil || core.CheckName(edit.Old.Name) != nil ||
		!validHelperEditID(edit.Old.ID) || !validHelperEditID(edit.Next) {
		return "", errors.New("invalid tunnel edit link")
	}
	if name != "" {
		if err := core.CheckName(name); err != nil {
			return "", err
		}
	}

	helperEditMu.Lock()
	defer helperEditMu.Unlock()
	match, err := findLinkedPeer(edit.Old, edit.Next, name, core.List(), core.LoadTunnelConfig)
	if err != nil {
		return "", err
	}
	beforeBody, err := os.ReadFile(app.ConfigPath(match.Name))
	if err != nil {
		return "", err
	}
	body, before, after, _, err := prepareHelperEdit(match.Name, string(beforeBody), edit.Changes, true)
	if err != nil {
		return "", err
	}
	id, side := peerRemovalIdentity(match, before)
	next, _ := peerRemovalIdentity(match, after)
	if side != "kharej" || (id != edit.Old.ID && id != edit.Next) || next != edit.Next {
		return "", fmt.Errorf("the edit does not match the settings of %q", match.Name)
	}
	if err := checkHelperEditPorts(before, after); err != nil {
		return "", err
	}
	return match.Name, writeHelperEdit(match.Name, string(beforeBody), body)
}

func validHelperEditID(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil
}
