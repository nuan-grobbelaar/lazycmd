// Package last remembers, per repo, the answers you last gave each
// command, and which command you ran last, so lazycmd can run it again
// with the same answers ("." for the last command, "R" for the selected
// one). Opening a form still starts from each command's defaults; this
// is only read when you ask to run something again.
//
// Secret params are never stored. Other answers can still be sensitive,
// so the file is readable by you only.
package last

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const fileName = "last.json"

// Entry is one command's last answers in one repo.
type Entry struct {
	Values map[string]string `json:"values"`
	At     time.Time         `json:"at"`
}

type repo struct {
	Last     string           `json:"last"` // id of the command run last here
	Commands map[string]Entry `json:"commands"`
}

// Store maps repo root to that repo's last answers.
type Store struct {
	Repos map[string]*repo `json:"repos"`
}

// Load reads the store from stateDir. A missing or corrupt file is an
// empty store: the cost is only that "run last" has nothing to offer.
func Load(stateDir string) *Store {
	s := &Store{Repos: map[string]*repo{}}
	if data, err := os.ReadFile(filepath.Join(stateDir, fileName)); err == nil {
		_ = json.Unmarshal(data, s)
	}
	if s.Repos == nil {
		s.Repos = map[string]*repo{}
	}
	for k, r := range s.Repos {
		if r == nil || r.Commands == nil {
			delete(s.Repos, k)
		}
	}
	return s
}

// Save writes the store, readable by the user only, via a temp file and
// rename so a crash can't leave it half written.
func (s *Store) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // WriteFile keeps an existing file's mode
		return err
	}
	return os.Rename(tmp, path)
}

// Record stores values as id's last answers in repoRoot and makes id the
// repo's last command. Params named in secrets are left out.
func (s *Store) Record(repoRoot, id string, values map[string]string, secrets []string, now time.Time) {
	skip := map[string]bool{}
	for _, n := range secrets {
		skip[n] = true
	}
	kept := map[string]string{}
	for k, v := range values {
		if !skip[k] {
			kept[k] = v
		}
	}
	r := s.Repos[repoRoot]
	if r == nil {
		r = &repo{Commands: map[string]Entry{}}
		s.Repos[repoRoot] = r
	}
	r.Last = id
	r.Commands[id] = Entry{Values: kept, At: now}
}

// Get returns id's last answers in repoRoot.
func (s *Store) Get(repoRoot, id string) (Entry, bool) {
	r := s.Repos[repoRoot]
	if r == nil {
		return Entry{}, false
	}
	e, ok := r.Commands[id]
	return e, ok
}

// Last returns the command run last in repoRoot and its answers.
func (s *Store) Last(repoRoot string) (string, Entry, bool) {
	r := s.Repos[repoRoot]
	if r == nil || r.Last == "" {
		return "", Entry{}, false
	}
	e, ok := r.Commands[r.Last]
	return r.Last, e, ok
}
