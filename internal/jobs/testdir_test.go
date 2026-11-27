package jobs

import (
	"testing"
	"time"
)

// testDir is t.TempDir that first stops and waits for jobs left running
// (see StopAll), so none is still writing into it when it's removed.
func testDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Cleanup(func() { StopAll(5 * time.Second) })
	return d
}
