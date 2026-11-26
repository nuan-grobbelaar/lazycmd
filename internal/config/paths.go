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

// WatchPaths lists the config files and folders whose changes mean the
// command list should be rebuilt: the global and the repo's config.yaml,
// the script folders and commands.yaml, the folder of profiles, and the
// matching profile's includes. (The scripts commands point at are added
// by the caller, from the commands it loaded.) Folders are watched by
// what's directly in them.
func WatchPaths(globalDir, projectDir string, profile *Profile) []string {
	paths := []string{
		filepath.Join(globalDir, "config.yaml"),
		filepath.Join(globalDir, "scripts"),
		ProfilesDir(globalDir),
		filepath.Join(projectDir, "config.yaml"),
		filepath.Join(projectDir, "commands.yaml"),
		filepath.Join(projectDir, "scripts"),
	}
	if profile != nil {
		base := filepath.Dir(profile.Source)
		for _, entry := range profile.File.Include {
			paths = append(paths, expandPath(entry, base))
		}
	}
	return paths
}
