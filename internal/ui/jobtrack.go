package ui

import (
	"lazycmd/internal/ask"
	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
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

	asks     []*ask.Pending // questions from lazycmd ask/pick, oldest first
	askToken string         // its LC_ASK_TOKEN, forgotten when it ends

	past *jobs.PastJob // set for an earlier run, listed from history
}
