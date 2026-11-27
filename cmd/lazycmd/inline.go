package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"lazycmd/internal/config"
	"lazycmd/internal/jobs"
	"lazycmd/internal/ui"
)

// runInline is `lazycmd <script> [args...]`: run one script in this
// terminal, without the TUI. A script with a sidecar (beside it, or in
// the matching profile's sidecars:) first shows its form inline, then
// any confirm question; args are then name=value presets for the form.
// A script without one runs straight away, with args passed through as
// they are. Either way lazycmd then replaces itself with the script
// (exec), so its output, Ctrl-C and exit status are the script's own,
// and `lazycmd ask` / `lazycmd pick` inside it ask on this terminal.
func runInline(globalDir, cwd, script string, args []string, interactive bool, stderr io.Writer) int {
	profile, _ := config.LoadProfile(globalDir, cwd)
	cmd, hasSidecar, err := config.InlineCommand(globalDir, cwd, script, profile)
	if err != nil {
		fmt.Fprintln(stderr, "lazycmd:", err)
		return 2
	}

	if !hasSidecar {
		return execScript(cmd.ScriptPath, append([]string{cmd.ScriptPath}, args...), os.Environ(), stderr)
	}

	if err := config.SetValues(&cmd, args); err != nil {
		fmt.Fprintf(stderr, "lazycmd: %s: %v\n", script, err)
		return 2
	}

	// The form shows the script as it was typed, not its absolute path.
	shown := cmd
	shown.ScriptPath = script

	var values map[string]string
	if interactive {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			fmt.Fprintln(stderr, "lazycmd:", err)
			return 1
		}
		values, err = ui.RunInlineForm(shown, cwd, tty, tty)
		tty.Close()
		if errors.Is(err, ui.ErrCancelled) {
			return 130
		}
		if err != nil {
			fmt.Fprintln(stderr, "lazycmd:", err)
			return 1
		}
	} else {
		// No terminal to show the form on: the defaults and name=value
		// arguments are the answers, and a confirm question can't be
		// answered, so it refuses rather than running unasked.
		problems, v := ui.FormProblems(shown, cwd)
		if len(problems) > 0 {
			fmt.Fprintf(stderr, "lazycmd: %s: %s (set them as name=value)\n", script, strings.Join(problems, "; "))
			return 2
		}
		if cmd.Confirm != "" {
			fmt.Fprintf(stderr, "lazycmd: %s asks %q before running, and there's no terminal to answer on\n", script, config.ExpandConfirm(cmd, v))
			return 2
		}
		values = v
	}

	ec, err := jobs.BuildCmd(cmd, values)
	if err != nil {
		fmt.Fprintln(stderr, "lazycmd:", err)
		return 1
	}
	return execScript(ec.Path, ec.Args, ec.Env, stderr)
}

// execScript replaces lazycmd with the script. It only returns if that
// fails.
func execScript(path string, argv, env []string, stderr io.Writer) int {
	err := syscall.Exec(path, argv, env)
	fmt.Fprintf(stderr, "lazycmd: running %s: %v\n", path, err)
	return 126
}
