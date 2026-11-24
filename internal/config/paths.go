package config

import (
	"os"
	"path/filepath"
)

// GlobalDir is ~/.config/lazycast, holding global settings and scripts.
func GlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lazycast"), nil
}

// StateDir is ~/.local/state/lazycast, holding run logs and the usage log.
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "lazycast"), nil
}
