package main

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lazycmd/internal/config"
	"lazycmd/internal/features"
	"lazycmd/internal/trust"
	"lazycmd/internal/ui"
	"lazycmd/internal/watch"
)

// app builds the command list for one repo. It does so at startup and
// again whenever the files it read change, so everything that depends on
// the files is read inside load, never kept from before.
type app struct {
	globalDir, stateDir, cwd, projectDir string

	// featureFiles is the files the features read, found by searching
	// folders, so it's looked up at most every few seconds.
	featureFiles func() []string

	mu          sync.Mutex
	watched     []string // paths the last load read
	profileName string   // the matching profile's name, "" if none
}

func newApp(globalDir, stateDir, cwd string) *app {
	return &app{
		globalDir: globalDir, stateDir: stateDir, cwd: cwd,
		projectDir:   filepath.Join(cwd, ".lazycmd"),
		featureFiles: watch.Cache(10*time.Second, func() []string { return features.WatchPaths(cwd) }),
	}
}

// load assembles the full command list, held back where the repo's own
// commands haven't been allowed.
func (a *app) load() ui.Loaded {
	// A central profile (~/.config/lazycmd/projects/*.yaml) can add
	// commands, set form defaults and tune features for this repo.
	profile, warnings := config.LoadProfile(a.globalDir, a.cwd)
	var included []config.ResolvedCommand
	var profileFeatures []config.FeaturesFile
	profileName := ""
	if profile != nil {
		var w []string
		included, w = profile.Includes(a.cwd)
		warnings = append(warnings, w...)
		profileFeatures = append(profileFeatures, profile.File.FeaturesFile)
		profileName = strings.TrimSuffix(filepath.Base(profile.Source), filepath.Ext(profile.Source))
	}

	// build assembles the full command list. With repoCommands false the
	// repo's own .lazycmd/ commands are left out (its config.yaml still
	// applies: it only switches features and settings, it runs nothing;
	// its maven goals are limited to plain goal names).
	build := func(repoCommands bool) ([]config.Group, []string) {
		dir := a.projectDir
		if !repoCommands {
			dir = ""
		}
		var w []string
		groups, discWarnings := config.DiscoverWith(a.globalDir, dir, included)
		w = append(w, discWarnings...)
		groups = append([]config.Group{config.AdhocGroup()}, groups...)

		enabled, settings, mavenGoals, featCfgWarnings := config.EnabledFeatures(a.globalDir, a.projectDir, profileFeatures...)
		featureGroups, featRunWarnings := features.Discover(a.cwd, enabled, settings, mavenGoals)
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
	repoCmds, _ := config.ProjectCommands(a.projectDir)
	groups := fullGroups
	var pending *ui.PendingTrust
	if len(repoCmds) > 0 {
		fingerprint := trust.Fingerprint(a.projectDir, repoCmds)
		if state := trust.Load(a.stateDir).Check(a.cwd, fingerprint); state != trust.Allowed {
			groups, _ = build(false)
			pending = &ui.PendingTrust{
				State:       state,
				Commands:    repoCmds,
				Fingerprint: fingerprint,
				FullGroups:  fullGroups,
			}
		}
	}

	// Watch the config, plus every script any listed command runs (from
	// anywhere: a profile can include scripts outside the repo).
	paths := config.WatchPaths(a.globalDir, a.projectDir, profile)
	for _, g := range fullGroups {
		for _, c := range g.Commands {
			if c.ScriptPath != "" {
				paths = append(paths, c.ScriptPath)
			}
		}
	}
	a.mu.Lock()
	a.watched, a.profileName = paths, profileName
	a.mu.Unlock()

	return ui.Loaded{Groups: groups, Warnings: warnings, Pending: pending}
}

// watchPaths is everything whose change should trigger a reload.
func (a *app) watchPaths() []string {
	a.mu.Lock()
	paths := append([]string(nil), a.watched...)
	a.mu.Unlock()
	return append(paths, a.featureFiles()...)
}

func (a *app) profile() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.profileName
}

// trustCheck is run right before a repo command starts: nil while the
// repo's commands are still the ones allowed, else a freshly loaded
// list, in which they are held back again.
func (a *app) trustCheck() *ui.Loaded {
	repoCmds, _ := config.ProjectCommands(a.projectDir)
	if len(repoCmds) > 0 && trust.Load(a.stateDir).Check(a.cwd, trust.Fingerprint(a.projectDir, repoCmds)) == trust.Allowed {
		return nil
	}
	l := a.load()
	return &l
}
