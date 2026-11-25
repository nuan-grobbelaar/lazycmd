package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"lazycmd/internal/config"
	"lazycmd/internal/features"
	"lazycmd/internal/trust"
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

	// build assembles the full command list. With repoCommands false the
	// repo's own .lazycmd/ commands are left out (its config.yaml still
	// applies: it only switches features and settings, it runs nothing).
	build := func(repoCommands bool) ([]config.Group, []string) {
		dir := projectDir
		if !repoCommands {
			dir = ""
		}
		var w []string
		groups, discWarnings := config.DiscoverWith(globalDir, dir, included)
		w = append(w, discWarnings...)
		groups = append([]config.Group{config.AdhocGroup()}, groups...)

		enabled, settings, featCfgWarnings := config.EnabledFeatures(globalDir, projectDir, profileFeatures...)
		featureGroups, featRunWarnings := features.Discover(cwd, enabled, settings)
		groups = append(groups, featureGroups...)
		w = append(w, featCfgWarnings...)
		w = append(w, featRunWarnings...)

		// Defaults go last so they can reach feature commands too.
		if profile != nil {
			w = append(w, config.ApplyDefaults(groups, profile.File.Defaults)...)
		}
		return groups, w
	}

	fullGroups, buildWarnings := build(true)
	warnings = append(warnings, buildWarnings...)

	// A repo's own commands are listed only once the user has allowed
	// them (and again after they change); see package trust.
	repoCmds, _ := config.ProjectCommands(projectDir)
	groups := fullGroups
	var pending *ui.PendingTrust
	if len(repoCmds) > 0 {
		fingerprint := trust.Fingerprint(projectDir, repoCmds)
		if state := trust.Load(stateDir).Check(cwd, fingerprint); state != trust.Allowed {
			groups, _ = build(false)
			pending = &ui.PendingTrust{
				State:       state,
				Commands:    repoCmds,
				Fingerprint: fingerprint,
				FullGroups:  fullGroups,
			}
		}
	}

	model := ui.New(groups, warnings, stateDir, cwd)
	if profile != nil {
		model = model.WithProfile(strings.TrimSuffix(filepath.Base(profile.Source), filepath.Ext(profile.Source)))
	}
	if pending != nil {
		model = model.WithPendingTrust(*pending)
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
