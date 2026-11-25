package usage

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"lazycmd/internal/config"
)

// halfLife is how long it takes a command's usage score to decay by
// half, so recent runs weigh more and old favourites don't stick.
const halfLife = 14 * 24 * time.Hour

const fileName = "usage.json"

type entry struct {
	Count    float64   `json:"count"`
	LastUsed time.Time `json:"last_used"`
}

// Log is a per-repo, per-command usage count that decays over time. A
// command's score is computed on read from Count and LastUsed, so
// there's no background aging job to run.
type Log struct {
	Repos map[string]map[string]entry `json:"repos"`
}

// Load reads the usage log from stateDir. A missing or corrupt file
// just starts fresh; losing usage history isn't worth failing over.
func Load(stateDir string) *Log {
	l := &Log{Repos: map[string]map[string]entry{}}
	data, err := os.ReadFile(filepath.Join(stateDir, fileName))
	if err == nil {
		_ = json.Unmarshal(data, l)
	}
	if l.Repos == nil {
		l.Repos = map[string]map[string]entry{}
	}
	return l
}

// Save writes the log to stateDir, via a temp file and rename so a
// crash mid-write can't corrupt it.
func (l *Log) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Record bumps id's score for repoRoot: whatever it had decays up to
// now, then gets +1 for this run.
func (l *Log) Record(repoRoot, id string, now time.Time) {
	if l.Repos[repoRoot] == nil {
		l.Repos[repoRoot] = map[string]entry{}
	}
	e := l.Repos[repoRoot][id]
	l.Repos[repoRoot][id] = entry{Count: decayedScore(e, now) + 1, LastUsed: now}
}

// Score is id's current decayed usage count for repoRoot, 0 if it's
// never been run there.
func (l *Log) Score(repoRoot, id string, now time.Time) float64 {
	return decayedScore(l.Repos[repoRoot][id], now)
}

func decayedScore(e entry, now time.Time) float64 {
	if e.Count == 0 {
		return 0
	}
	elapsed := now.Sub(e.LastUsed)
	if elapsed <= 0 {
		return e.Count
	}
	halfLives := elapsed.Hours() / halfLife.Hours()
	return e.Count * math.Pow(0.5, halfLives)
}

// SortByUsage stable-sorts cmds by descending score for repoRoot, so
// ties - most commonly everything at zero - keep the order they came
// in with (callers pre-sort alphabetically).
func (l *Log) SortByUsage(cmds []config.ResolvedCommand, repoRoot string, now time.Time) {
	sort.SliceStable(cmds, func(i, j int) bool {
		return l.Score(repoRoot, cmds[i].ID, now) > l.Score(repoRoot, cmds[j].ID, now)
	})
}
