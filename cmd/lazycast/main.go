package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"lazycast/internal/config"
	"lazycast/internal/features"
	"lazycast/internal/ui"
)

func main() {
	globalDir, err := config.GlobalDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycast:", err)
		os.Exit(1)
	}
	stateDir, err := config.StateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycast:", err)
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycast:", err)
		os.Exit(1)
	}

	projectDir := filepath.Join(cwd, ".lazycast")
	groups, warnings := config.Discover(globalDir, projectDir)

	enabled, settings, featCfgWarnings := config.EnabledFeatures(globalDir, projectDir)
	featureGroups, featRunWarnings := features.Discover(cwd, enabled, settings)
	groups = append(groups, featureGroups...)
	warnings = append(warnings, featCfgWarnings...)
	warnings = append(warnings, featRunWarnings...)

	p := tea.NewProgram(ui.New(groups, warnings, stateDir, cwd), tea.WithAltScreen())
	finalModel, err := p.Run()
	if m, ok := finalModel.(ui.Model); ok {
		m.Shutdown()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycast:", err)
		os.Exit(1)
	}
}
