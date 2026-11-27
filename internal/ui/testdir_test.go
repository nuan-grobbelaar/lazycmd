package ui

import (
	"testing"
	"time"

	"lazycmd/internal/jobs"
)

// testDir is t.TempDir for these tests: before the folder is removed,
// any job a test left running is stopped and waited for, so it isn't
// still writing its log or history into the folder as it goes.
func testDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Cleanup(func() { jobs.StopAll(5 * time.Second) }) // runs before the removal
	return d
}
