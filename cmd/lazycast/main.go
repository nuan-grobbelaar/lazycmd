package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"lazycast/internal/config"
	"lazycast/internal/ui"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycast:", err)
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycast:", err)
		os.Exit(1)
	}

	globalDir := filepath.Join(home, ".config", "lazycast")
	projectDir := filepath.Join(cwd, ".lazycast")
	groups, warnings := config.Discover(globalDir, projectDir)

	p := tea.NewProgram(ui.New(groups, warnings), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazycast:", err)
		os.Exit(1)
	}
}
