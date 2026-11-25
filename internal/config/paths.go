package config

import (
	"os"
	"path/filepath"
)

// GlobalDir is ~/.config/lazycmd, holding global settings and scripts.
func GlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lazycmd"), nil
}

// StateDir is ~/.local/state/lazycmd, holding run logs and the usage log.
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "lazycmd"), nil
}
