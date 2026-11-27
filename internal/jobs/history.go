package jobs

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// History: each finished panel job adds one line to
// stateDir/history.jsonl (readable only by you) naming its command, the
// repo it ran in, when, how it ended and its log file. Earlier runs are
// listed from it, and their output read back from the logs. A run whose
// log has been pruned (see PrepareState) is left out.

const historyFile = "history.jsonl"

// maxPastOutput is how much of a past run's log is shown: its end, which
// is where the result and any error are.
const maxPastOutput = 4 << 20

type historyRun struct {
	CmdID      string    `json:"cmd_id"`
	Name       string    `json:"name"`
	Repo       string    `json:"repo"`
	Log        string    `json:"log"`
	Started    time.Time `json:"started"`
	DurationMS int64     `json:"duration_ms"`
	Status     string    `json:"status"`
	ExitCode   int       `json:"exit_code"`
}

func appendHistory(stateDir string, run historyRun) {
	data, err := json.Marshal(run)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(stateDir, historyFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n')) // one write, so concurrent lazycmds don't interleave lines
}

// readHistory returns every recorded run whose log still exists, oldest
// first. Lines that don't parse are skipped.
func readHistory(stateDir string) []historyRun {
	f, err := os.Open(filepath.Join(stateDir, historyFile))
	if err != nil {
		return nil
	}
	defer f.Close()
	var runs []historyRun
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r historyRun
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Log == "" || filepath.Base(r.Log) != r.Log {
			continue
		}
		if _, err := os.Stat(filepath.Join(stateDir, "logs", r.Log)); err != nil {
			continue
		}
		runs = append(runs, r)
	}
	return runs
}

// compactHistory rewrites the history file with only the runs whose log
// still exists, so it doesn't grow forever. Called after logs are pruned.
func compactHistory(stateDir string) {
	runs := readHistory(stateDir)
	path := filepath.Join(stateDir, historyFile)
	if _, err := os.Stat(path); err != nil {
		return
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for _, r := range runs {
		if data, err := json.Marshal(r); err == nil {
			w.Write(append(data, '\n'))
		}
	}
	if w.Flush() != nil || f.Close() != nil {
		os.Remove(tmp)
		return
	}
	_ = os.Rename(tmp, path)
}

// PastJob is an earlier run, as a finished Job whose output is loaded
// from its log on demand (Load).
type PastJob struct {
	*Job
	CmdID   string
	Started time.Time
	loaded  bool
}

// History returns repo's earlier runs, newest first, at most limit,
// leaving out the logs in skip (this session's own jobs, already listed).
func History(stateDir, repo string, limit int, skip map[string]bool) []*PastJob {
	runs := readHistory(stateDir)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Started.After(runs[j].Started) })
	var out []*PastJob
	for _, r := range runs {
		if r.Repo != repo || skip[r.Log] {
			continue
		}
		status := StatusFailed
		if r.Status == StatusSucceeded.String() {
			status = StatusSucceeded
		}
		j := &Job{
			ID: r.Log, Name: r.Name, Buffer: &OutputBuffer{},
			status: status, exitCode: r.ExitCode,
			startedAt: r.Started, endedAt: r.Started.Add(time.Duration(r.DurationMS) * time.Millisecond),
			closed: true, past: true, LogName: r.Log, stateDir: stateDir,
		}
		out = append(out, &PastJob{Job: j, CmdID: r.CmdID, Started: r.Started})
		if len(out) == limit {
			break
		}
	}
	return out
}

// Load reads the run's output from its log into its Buffer, once: the
// last maxPastOutput bytes of it, through the same filter as live
// output, so only colours survive.
func (p *PastJob) Load() {
	if p.loaded {
		return
	}
	p.loaded = true
	f, err := os.Open(filepath.Join(p.stateDir, "logs", p.LogName))
	if err != nil {
		p.Buffer.Write([]byte("(the log of this run is gone)\n"))
		return
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > maxPastOutput {
		_, _ = f.Seek(info.Size()-maxPastOutput, io.SeekStart)
		p.Buffer.Write([]byte("(only the end of this run's output is shown)\n"))
	}
	data, _ := io.ReadAll(io.LimitReader(f, maxPastOutput))
	p.Buffer.Write(data)
	p.Buffer.Touch()
}

// Past reports whether j was loaded from history rather than started.
func (j *Job) Past() bool { return j.past }
