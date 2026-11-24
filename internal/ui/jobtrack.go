package ui

import (
	"lazycast/internal/config"
	"lazycast/internal/jobs"
)

// trackedJob pairs a running or finished job with the UI-only state
// needed to list and manage it: what was run (so the jobs panel can
// restart it identically), and whether it's currently sitting on a
// prompt that hasn't been surfaced yet. A background job's needsInput
// shows as a badge in the jobs panel instead of popping a dialog and
// stealing focus - only the active job's prompt ever does that.
type trackedJob struct {
	job    *jobs.Job
	cmd    config.ResolvedCommand
	values map[string]string

	needsInput       bool
	dismissedForText string
}
