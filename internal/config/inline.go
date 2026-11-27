package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InlineCommand is the command for `lazycmd <script>`: the script at path
// with its sidecar, if it has one. A sidecars: entry in the matching
// profile (for a script inside repoRoot) wins over a sidecar beside the
// script, as it does in the TUI. The profile's params:, defaults: and
// confirm: for the command apply too. hasSidecar is false when neither
// exists: the script then runs as is, with no form.
func InlineCommand(globalDir, repoRoot, path string, profile *Profile) (cmd ResolvedCommand, hasSidecar bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return cmd, false, err
	}
	info, err := os.Stat(abs)
	switch {
	case err != nil:
		return cmd, false, fmt.Errorf("%s: no such script", path)
	case !info.Mode().IsRegular():
		return cmd, false, fmt.Errorf("%s: not a file", path)
	case info.Mode()&0o111 == 0:
		return cmd, false, fmt.Errorf("%s: not executable (chmod +x it)", path)
	}

	dir, file := filepath.Split(abs)
	dir = filepath.Clean(dir)
	scope := "project"
	if dir == filepath.Join(globalDir, "scripts") {
		scope = "global"
	}

	found := false
	if profile != nil {
		c, ok, err := profile.RepoSidecar(repoRoot, abs)
		if err != nil {
			return cmd, false, fmt.Errorf("%s (sidecars): %v", profile.Source, err)
		}
		cmd, found = c, ok
	}
	if !found {
		name := strings.TrimSuffix(file, filepath.Ext(file))
		_, statErr := os.Stat(filepath.Join(dir, name+sidecarSuffix))
		found = statErr == nil
		if cmd, err = scriptCommand(dir, file, scope); err != nil {
			return cmd, false, err
		}
	}

	if profile != nil {
		// One command, so warnings about the profile's other entries
		// ("no command named ...") don't apply here.
		groups := []Group{{Commands: []ResolvedCommand{cmd}}}
		ApplyParamOverrides(groups, profile.File.Params)
		ApplyConfirm(groups, profile.File.Confirm)
		ApplyDefaults(groups, profile.File.Defaults)
		cmd = groups[0].Commands[0]
	}
	return cmd, found, nil
}

// SetValues presets params from name=value arguments, the way a profile's
// defaults: do: the form starts with them, and without a form (no
// terminal) they are the answers. A name the command doesn't have, or a
// value that isn't one of a select's options, is an error.
func SetValues(cmd *ResolvedCommand, assignments []string) error {
	cmd.Params = append([]Param(nil), cmd.Params...)
	for _, a := range assignments {
		name, value, ok := strings.Cut(a, "=")
		if !ok || name == "" {
			return fmt.Errorf("%q: expected name=value", a)
		}
		if w := setDefault(cmd, name, value); len(w) > 0 {
			return fmt.Errorf("%s", strings.TrimPrefix(w[0], "defaults: "))
		}
	}
	return nil
}
