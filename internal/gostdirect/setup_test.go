package gostdirect

import (
	"encoding/base64"
	"testing"
)

func TestSetupTokenChoosesOnlyItsOwnGOSTVersion(t *testing.T) {
	for _, tc := range []struct {
		prefix, payload string
		want            int
	}{
		{"bpgost1.", `{"name":"peer","target":8080}`, 3},
		{"bpgost2.", `{"name":"peer","target":8080,"version":1}`, 1},
		{"bpgost2.", `{"name":"peer","target":8080,"version":2}`, 2},
		{"bpgost1.", `{"name":"peer","target":8080,"version":2}`, 0},
		{"bpgost2.", `{"name":"peer","target":8080}`, 0},
	} {
		raw := tc.prefix + base64.StdEncoding.EncodeToString([]byte(tc.payload))
		got, err := ReadTokenInfo(raw)
		if tc.want == 0 {
			if err == nil {
				t.Fatalf("accepted a mismatched GOST setup token: %s %s", tc.prefix, tc.payload)
			}
			continue
		}
		if err != nil || got.Version != tc.want || got.Name != "peer" || got.Target != 8080 {
			t.Fatalf("GOST v%d setup token decoded as %+v: %v", tc.want, got, err)
		}
	}
}
