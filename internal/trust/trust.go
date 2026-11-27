// Package trust remembers which repos' own commands (.lazycmd/) the user
// has allowed, the way direnv's `allow` does for .envrc.
//
// A repo you cloned decides what its .lazycmd/ commands are called and
// what they run, and selecting one runs it. So the first time a repo
// defines commands, and again whenever they change, lazycmd asks before
// it lists them. What is remembered is a fingerprint of everything those
// commands consist of: commands.yaml, every file in scripts/, and every
// script a command points at, wherever it lives. Changing any byte of
// that (a `git pull` that edits a script, say) asks again.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"lazycmd/internal/config"
	"lazycmd/internal/safefile"
)

const fileName = "trust.json"

// State is what the store knows about a repo's current commands.
type State int

const (
	Unknown State = iota // never allowed
	Changed              // allowed once, but the commands have changed since
	Allowed              // allowed, and unchanged since
)

// Store maps a repo's resolved path to the fingerprint the user allowed.
type Store struct {
	Repos map[string]string `json:"repos"`
}

// Load reads the store from stateDir. A missing or unreadable file is an
// empty store: the cost is being asked again, never being let through.
func Load(stateDir string) *Store {
	s := &Store{Repos: map[string]string{}}
	if data, err := os.ReadFile(filepath.Join(stateDir, fileName)); err == nil {
		_ = json.Unmarshal(data, s)
	}
	if s.Repos == nil {
		s.Repos = map[string]string{}
	}
	return s
}

// Save writes the store, readable by the user only, via a temp file and
// rename so a crash can't leave it half written.
func (s *Store) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Check reports whether fingerprint is the one allowed for repo.
func (s *Store) Check(repo, fingerprint string) State {
	got, ok := s.Repos[RepoKey(repo)]
	switch {
	case !ok:
		return Unknown
	case got != fingerprint:
		return Changed
	default:
		return Allowed
	}
}

// Allow records fingerprint as allowed for repo.
func (s *Store) Allow(repo, fingerprint string) {
	s.Repos[RepoKey(repo)] = fingerprint
}

// RepoKey is the store key for a repo: its absolute path with symlinks
// resolved, so the same checkout reached two ways is one entry.
func RepoKey(repo string) string {
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	return filepath.Clean(repo)
}

// Fingerprint hashes everything the repo's commands consist of:
// commands.yaml, each file directly in scripts/ (name, executable bit
// and contents, which covers sidecars), and the contents of every script
// a command points at, since `script:` may name a file outside
// .lazycmd/. Files are hashed in a fixed order, so the result depends
// only on what is on disk. A file that can't be read is hashed as
// unreadable rather than skipped, so it still counts as a change.
func Fingerprint(projectDir string, cmds []config.ResolvedCommand) string {
	h := sha256.New()
	add := func(label, path string) {
		fmt.Fprintf(h, "%s\x00%s\x00", label, path)
		f, err := safefile.Open(path) // a FIFO or /dev/zero must not hang this
		if err != nil {
			fmt.Fprintf(h, "unreadable\x00")
			return
		}
		defer f.Close()
		n, _ := io.Copy(h, safefile.Reader(f))
		fmt.Fprintf(h, "\x00%d\x00", n)
	}

	add("commands", filepath.Join(projectDir, "commands.yaml"))

	scriptsDir := filepath.Join(projectDir, "scripts")
	if entries, err := os.ReadDir(scriptsDir); err == nil {
		for _, e := range entries { // ReadDir sorts by name
			if e.IsDir() {
				continue
			}
			mode := "-"
			if info, err := e.Info(); err == nil && info.Mode()&0o111 != 0 {
				mode = "x"
			}
			add("script-"+mode, filepath.Join(scriptsDir, e.Name()))
		}
	}

	var targets []string
	for _, c := range cmds {
		if c.ScriptPath != "" {
			targets = append(targets, filepath.Clean(c.ScriptPath))
		}
	}
	sort.Strings(targets)
	for i, t := range targets {
		if i > 0 && targets[i-1] == t {
			continue
		}
		add("target", t)
	}
	return hex.EncodeToString(h.Sum(nil))
}
