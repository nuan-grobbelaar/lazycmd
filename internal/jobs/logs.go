package jobs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KeepLogs is how many run logs PrepareState keeps.
const KeepLogs = 200

// PrepareState makes stateDir and its logs folder private (0700) and
// keeps only the newest keep logs. The folders may have been created
// world-readable by an older lazycmd, and MkdirAll leaves an existing
// folder's mode alone, so the mode is set explicitly. Logs hold
// everything commands printed, so other users on the machine must not
// read them, and without pruning they'd pile up forever. Best effort.
func PrepareState(stateDir string, keep int) {
	logs := filepath.Join(stateDir, "logs")
	_ = os.MkdirAll(logs, 0o700)
	_ = os.Chmod(stateDir, 0o700)
	_ = os.Chmod(logs, 0o700)

	entries, err := os.ReadDir(logs)
	if err != nil {
		return
	}
	type logFile struct {
		path string
		mod  int64
	}
	var files []logFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, logFile{filepath.Join(logs, e.Name()), info.ModTime().UnixNano()})
	}
	if len(files) > keep {
		sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
		for _, f := range files[keep:] {
			_ = os.Remove(f.path)
		}
	}
	compactHistory(stateDir)
}
