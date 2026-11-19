package webui

import (
    "bytes"
    "net/http"
    "strings"

    "github.com/backpack/backpack/internal/omni"
)

// Keep the same argument validation and pinned executable as the CLI.
func (s *server) handleOmni(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Cache-Control", "no-store")
    if r.Method == http.MethodGet {
        writeJSON(w, omni.Options())
        return
    }
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    var form struct {
        Args []string `json:"args"`
        Token string `json:"token"`
    }
    if err := decodeJSON(w, r, &form); err != nil || len(form.Args) == 0 || len(form.Args) > 5 {
        http.Error(w, "invalid OmniTunnel operation", http.StatusBadRequest)
        return
    }
    var out bytes.Buffer
    if err := omni.Run(form.Args, strings.NewReader(form.Token), &out, &out); err != nil {
        http.Error(w, strings.TrimSpace(out.String()+"\n"+err.Error()), http.StatusBadRequest)
        return
    }
    writeJSON(w, map[string]string{"output":out.String()})
}
