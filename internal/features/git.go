package features

import (
	"path/filepath"

	"lazycast/internal/config"
)

type Git struct{}

func (Git) Name() string { return "git" }

func (Git) Detect(repoRoot string) (bool, string) {
	if !fileExists(filepath.Join(repoRoot, ".git")) {
		return false, ""
	}
	return true, ".git"
}

func (Git) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	actions := []struct{ name, run string }{
		{"fetch --prune", "git fetch --prune"},
		{"pull", "git pull"},
		{"push", "git push"},
		{"stash", "git stash"},
		{"stash pop", "git stash pop"},
		{"worktree list", "git worktree list"},
	}
	cmds := make([]config.ResolvedCommand, 0, len(actions))
	for _, a := range actions {
		cmds = append(cmds, config.ResolvedCommand{
			ID:   namespacedID("git", a.name),
			Name: namespacedName("git", a.name),
			Mode: config.ModePanel,
			Run:  a.run,
		})
	}
	return cmds, nil
}
