package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalDirAndStateDirAreUnderHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available in this environment")
	}

	global, err := GlobalDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "lazycast"); global != want {
		t.Errorf("GlobalDir() = %q, want %q", global, want)
	}

	state, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "lazycast"); state != want {
		t.Errorf("StateDir() = %q, want %q", state, want)
	}
}
