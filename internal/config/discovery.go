package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const sidecarSuffix = ".lazycmd.yaml"

// ResolvedCommand is a command spec after merging a script with its
// optional sidecar, or after reading a commands.yaml entry. It's the
// shape the UI and, later, the runner work with.
type ResolvedCommand struct {
	// ID is stable across runs (e.g. "global:deploy-staging",
	// "project:deploy"), for keying the usage log.
	ID         string
	Name       string
	Desc       string
	Params     []Param
	Run        string
	ScriptPath string
	Args       []string // script arguments, with {param} placeholders; see args.go
	Terminal   bool     // enter runs it with the real terminal, like t
	Confirm    string   // question asked before it runs, {param} placeholders; "" asks nothing

	// Shadows is the path of the global script this project command
	// hides because it has the same name; empty when it hides nothing.
	// Set by discovery, never decoded from YAML. The UI marks such a
	// command and warns before its first run, since a repo you cloned
	// can otherwise take over the name of a script you run by habit.
	Shadows string

	// Preview, when set (feature commands only, never decoded from
	// YAML), renders the command line the params form shows for the
	// current answers. Needed where Run builds its arguments
	// conditionally, which plain $LC_ substitution can't show.
	Preview func(values map[string]string) string
}

type Group struct {
	Title    string
	Commands []ResolvedCommand
}

// Discover loads global scripts from globalDir/scripts, and, if projectDir
// exists, project scripts from projectDir/scripts plus commands from
// projectDir/commands.yaml. A project command shadows a global command
// with the same name, so the global one is dropped from its group.
// Problems in individual files (a bad sidecar, an invalid commands.yaml
// entry) are collected as warnings and skip just that file or entry;
// they never abort discovery.
func Discover(globalDir, projectDir string) (groups []Group, warnings []string) {
	return DiscoverWith(globalDir, projectDir, nil)
}

// DiscoverWith is Discover plus extra project commands from elsewhere
// (the files a central project profile includes). The repo's own
// .lazycmd/ commands win over an extra command of the same name.
func DiscoverWith(globalDir, projectDir string, extra []ResolvedCommand) (groups []Group, warnings []string) {
	globalCmds, warnings := scanScripts(filepath.Join(globalDir, "scripts"), "global", warnings)

	var projectCmds []ResolvedCommand
	if info, err := os.Stat(projectDir); err == nil && info.IsDir() {
		var scriptWarnings []string
		projectCmds, scriptWarnings = scanScripts(filepath.Join(projectDir, "scripts"), "project", nil)
		warnings = append(warnings, scriptWarnings...)

		fileCmds, fileWarnings := loadCommandsFile(projectDir)
		warnings = append(warnings, fileWarnings...)
		projectCmds = append(projectCmds, fileCmds...)
	}
	have := make(map[string]bool, len(projectCmds))
	byScript := make(map[string]int, len(projectCmds))
	for i, c := range projectCmds {
		have[c.Name] = true
		if c.ScriptPath != "" {
			byScript[filepath.Clean(c.ScriptPath)] = i
		}
	}
	for _, c := range extra {
		// A profile sidecar for a script the repo already lists gives
		// that script its form: it replaces the repo's entry.
		if i, ok := byScript[filepath.Clean(c.ScriptPath)]; ok && c.ScriptPath != "" {
			projectCmds[i] = c
			continue
		}
		if have[c.Name] {
			warnings = append(warnings, fmt.Sprintf("included command %q (%s) skipped: the project already defines one with that name", c.Name, c.ScriptPath))
			continue
		}
		have[c.Name] = true
		projectCmds = append(projectCmds, c)
	}

	globalByName := make(map[string]string, len(globalCmds))
	for _, c := range globalCmds {
		globalByName[c.Name] = c.ScriptPath
	}
	shadowed := make(map[string]bool, len(projectCmds))
	for i := range projectCmds {
		c := &projectCmds[i]
		shadowed[c.Name] = true
		if path, ok := globalByName[c.Name]; ok {
			c.Shadows = path
		}
	}
	var global []ResolvedCommand
	for _, c := range globalCmds {
		if !shadowed[c.Name] {
			global = append(global, c)
		}
	}

	sortByName(global)
	sortByName(projectCmds)

	if len(global) > 0 {
		groups = append(groups, Group{Title: "Global", Commands: global})
	}
	if len(projectCmds) > 0 {
		groups = append(groups, Group{Title: "This project", Commands: projectCmds})
	}
	return groups, warnings
}

// ProjectCommands loads only the repo's own commands from projectDir
// (scripts/ plus commands.yaml), the set a trust decision covers. It
// reads files and runs nothing. Nil when projectDir doesn't exist.
func ProjectCommands(projectDir string) ([]ResolvedCommand, []string) {
	if info, err := os.Stat(projectDir); err != nil || !info.IsDir() {
		return nil, nil
	}
	cmds, warnings := scanScripts(filepath.Join(projectDir, "scripts"), "project", nil)
	fileCmds, fileWarnings := loadCommandsFile(projectDir)
	return append(cmds, fileCmds...), append(warnings, fileWarnings...)
}

func sortByName(cmds []ResolvedCommand) {
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
}

// scanScripts finds executable files directly under dir and upgrades each
// with its sidecar, if one exists next to it. scope ("global" or
// "project") prefixes the resulting command IDs.
func scanScripts(dir, scope string, warnings []string) ([]ResolvedCommand, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, warnings
	}

	var cmds []ResolvedCommand
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), sidecarSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&0o111 == 0 {
			continue
		}

		cmd, err := scriptCommand(dir, entry.Name(), scope)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		cmds = append(cmds, cmd)
	}
	return cmds, warnings
}

// scriptCommand builds the command for the script file dir/file,
// upgraded by a sidecar (<name>.lazycmd.yaml) next to it if one exists.
func scriptCommand(dir, file, scope string) (ResolvedCommand, error) {
	name := strings.TrimSuffix(file, filepath.Ext(file))
	cmd := ResolvedCommand{ID: scope + ":" + name, Name: name, ScriptPath: filepath.Join(dir, file)}

	sidecarPath := filepath.Join(dir, name+sidecarSuffix)
	if _, err := os.Stat(sidecarPath); err == nil {
		var spec Command
		if err := decodeStrict(sidecarPath, &spec); err != nil {
			return cmd, fmt.Errorf("%s: %v", sidecarPath, err)
		}
		applySpec(&cmd, spec)
		if err := validateCommand(cmd); err != nil {
			return cmd, fmt.Errorf("%s: %v", sidecarPath, err)
		}
	}
	return cmd, nil
}

// loadCommandsFile reads projectDir/commands.yaml. A command entry that
// doesn't point at exactly one of run/script is skipped with a warning;
// the rest of the file still loads.
func loadCommandsFile(projectDir string) ([]ResolvedCommand, []string) {
	path := filepath.Join(projectDir, "commands.yaml")
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	return loadCommandsFileAt(path)
}

// loadCommandsFileAt reads a commands.yaml-shaped file; relative
// script: paths in it resolve against the file's own directory.
func loadCommandsFileAt(path string) ([]ResolvedCommand, []string) {
	projectDir := filepath.Dir(path)

	var file CommandsFile
	if err := decodeStrict(path, &file); err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", path, err)}
	}

	var warnings []string
	var cmds []ResolvedCommand
	for name, spec := range file.Commands {
		hasRun := spec.Run != ""
		hasScript := spec.Script != ""
		if hasRun == hasScript {
			warnings = append(warnings, fmt.Sprintf("%s: command %q must set exactly one of run or script", path, name))
			continue
		}
		cmd := ResolvedCommand{ID: "project:" + name, Name: name}
		applySpec(&cmd, spec)
		if hasScript {
			cmd.ScriptPath = expandPath(spec.Script, projectDir)
		}
		if err := validateCommand(cmd); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: command %q: %v", path, name, err))
			continue
		}
		cmds = append(cmds, cmd)
	}
	return cmds, warnings
}

func applySpec(cmd *ResolvedCommand, spec Command) {
	cmd.Desc = spec.Desc
	cmd.Params = spec.Params
	cmd.Run = spec.Run
	cmd.Args = spec.Args
	cmd.Terminal = spec.Terminal
	cmd.Confirm = confirmText(spec.Confirm, cmd.Name)
}
