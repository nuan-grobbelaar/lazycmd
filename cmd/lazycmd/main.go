package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"lazycmd/internal/ask"
	"lazycmd/internal/config"
	"lazycmd/internal/features"
	"lazycmd/internal/trust"
	"lazycmd/internal/ui"
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
	// applies: it only switches features and settings, it runs nothing;
	// its maven goals are limited to plain goal names).
	build := func(repoCommands bool) ([]config.Group, []string) {
		dir := projectDir
		if !repoCommands {
			dir = ""
		}
		var w []string
		groups, discWarnings := config.DiscoverWith(globalDir, dir, included)
		w = append(w, discWarnings...)
		groups = append([]config.Group{config.AdhocGroup()}, groups...)

		enabled, settings, mavenGoals, featCfgWarnings := config.EnabledFeatures(globalDir, projectDir, profileFeatures...)
		featureGroups, featRunWarnings := features.Discover(cwd, enabled, settings, mavenGoals)
		groups = append(groups, featureGroups...)
		w = append(w, featCfgWarnings...)
		w = append(w, featRunWarnings...)

		// Overrides and defaults go last so they can reach feature
		// commands too; defaults after params, so they win.
		if profile != nil {
			w = append(w, config.ApplyParamOverrides(groups, profile.File.Params)...)
			w = append(w, config.ApplyTerminal(groups, profile.File.Terminal)...)
			w = append(w, config.ApplyConfirm(groups, profile.File.Confirm)...)
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
