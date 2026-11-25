package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
	"lazycmd/internal/features"
	"lazycmd/internal/ui"
)

func main() {
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

	projectDir := filepath.Join(cwd, ".lazycmd")

	// A central profile (~/.config/lazycmd/projects/*.yaml) can add
	// commands, set form defaults and tune features for this repo.
	profile, warnings := config.LoadProfile(globalDir, cwd)
	var included []config.ResolvedCommand
	var profileFeatures []config.FeaturesFile
	if profile != nil {
		var w []string
		included, w = profile.Includes()
		warnings = append(warnings, w...)
		profileFeatures = append(profileFeatures, profile.File.FeaturesFile)
	}

	groups, discWarnings := config.DiscoverWith(globalDir, projectDir, included)
	warnings = append(warnings, discWarnings...)
	groups = append([]config.Group{config.AdhocGroup()}, groups...)

	enabled, settings, featCfgWarnings := config.EnabledFeatures(globalDir, projectDir, profileFeatures...)
	featureGroups, featRunWarnings := features.Discover(cwd, enabled, settings)
	groups = append(groups, featureGroups...)
	warnings = append(warnings, featCfgWarnings...)
	warnings = append(warnings, featRunWarnings...)

	// Defaults go last so they can reach feature commands too.
	if profile != nil {
		warnings = append(warnings, config.ApplyDefaults(groups, profile.File.Defaults)...)
	}

	model := ui.New(groups, warnings, stateDir, cwd)
	if profile != nil {
		model = model.WithProfile(strings.TrimSuffix(filepath.Base(profile.Source), filepath.Ext(profile.Source)))
	}
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if m, ok := finalModel.(ui.Model); ok {
		m.Shutdown()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazycmd:", err)
		os.Exit(1)
	}
}
