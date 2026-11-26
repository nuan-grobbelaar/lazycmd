package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"lazycmd/internal/ask"
	"lazycmd/internal/config"
	"lazycmd/internal/ui"
	"lazycmd/internal/watch"
)

func main() {
	// `lazycmd ask` / `lazycmd pick`: a script asking a question, not
	// the TUI. See package ask.
	if len(os.Args) > 1 && (os.Args[1] == ask.KindAsk || os.Args[1] == ask.KindPick) {
		os.Exit(ask.Main(os.Args[1], os.Args[2:], os.Stdin, term.IsTerminal(os.Stdin.Fd()), os.Stdout, os.Stderr))
	}

	globalDir, err := config.GlobalDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycmd:", err)
		os.Exit(1)
	}
	stateDir, err := config.StateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycmd:", err)
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycmd:", err)
		os.Exit(1)
	}

	a := newApp(globalDir, stateDir, cwd)
	loaded := a.load()

	model := ui.New(loaded.Groups, loaded.Warnings, stateDir, cwd)
	if name := a.profile(); name != "" {
		model = model.WithProfile(name)
	}
	if loaded.Pending != nil {
		model = model.WithPendingTrust(*loaded.Pending)
	}
	// The command list is rebuilt when the files it was built from change.
	model = model.WithReload(ui.FileReloader{Watcher: watch.New(a.watchPaths), Load: a.load})
	// Questions from jobs (lazycmd ask/pick) arrive on this socket. If it
	// can't be set up, scripts fall back to asking on a terminal.
	srv, err := ask.NewServer(stateDir)
	if err == nil {
		model = model.WithAsk(srv)
	} else {
		model = model.WithWarning("lazycmd ask/pick popups are unavailable: " + err.Error())
	}
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if m, ok := finalModel.(ui.Model); ok {
		m.Shutdown()
	}
	if srv != nil {
		srv.Close() // before any os.Exit, which would skip a defer
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycmd:", err)
		os.Exit(1)
	}
}
