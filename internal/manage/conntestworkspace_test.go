package manage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConnTestWorkspaceReapsAbandonedRunsAndKeepsActiveOnes(t *testing.T) {
	temp := t.TempDir()
	active, err := ctNewWorkspace(temp)
	if err != nil {
		t.Fatal(err)
	}
	defer active.close()
	abandoned, err := ctNewWorkspace(temp)
	if err != nil {
		t.Fatal(err)
	}
	defer abandoned.close()
	if err := os.Mkdir(filepath.Join(abandoned.dir, "helper"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abandoned.dir, "helper", "engine.log"), []byte("test output"), 0600); err != nil {
		t.Fatal(err)
	}
	// An interrupted process releases its lease without running close.
	if err := abandoned.lock.Close(); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(active.dir), "notes")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	next, err := ctNewWorkspace(temp)
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	if _, err := os.Stat(abandoned.dir); !os.IsNotExist(err) {
		t.Fatalf("abandoned workspace remains: %v", err)
	}
	for _, path := range []string{active.dir, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	next.close()
	if _, err := os.Stat(next.dir); !os.IsNotExist(err) {
		t.Fatalf("completed workspace remains: %v", err)
	}
	if err := os.Remove(unrelated); err != nil {
		t.Fatal(err)
	}
	active.close()
	if _, err := os.Stat(filepath.Dir(active.dir)); !os.IsNotExist(err) {
		t.Fatalf("empty workspace registry remains: %v", err)
	}
}
