package features

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"lazycmd/internal/config"
)

// A repo can hold more than one Maven project: a monorepo with
// independent poms in subfolders, with or without a pom at the root.
// Commands then pick a target ("project::module") and run mvn -f on that
// project. A repo with just a root project keeps the simpler single
// project form (module / also_make params) in maven.go.

type mavenProject struct {
	dir     string // relative to the repo root; "." for the root
	pom     pomXML
	modules []mavenModule // paths relative to dir
}

// hasNestedPom reports whether any pom.xml sits below the root, within
// the search limits. It only looks at names.
func hasNestedPom(repoRoot string) bool {
	found := false
	walkPoms(repoRoot, func(rel string) bool {
		if rel != "." {
			found = true
		}
		return !found
	})
	return found
}

// walkPoms calls visit with the folder (relative, slash-separated) of
// each pom.xml found, in sorted order, until visit returns false. See
// walkFor for what is searched.
func walkPoms(repoRoot string, visit func(rel string) bool) {
	walkFor(repoRoot, func(name string) bool { return name == "pom.xml" },
		func(rel, _ string) bool { return visit(rel) })
}

// findProjects returns the Maven projects of the repo: every pom found
// that isn't a module of another. A module pom belongs to its aggregator,
// so it's offered as a module there, not as a project of its own.
func findProjects(repoRoot string) []mavenProject {
	type found struct {
		dir string
		pom pomXML
	}
	var all []found
	walkPoms(repoRoot, func(rel string) bool {
		pom, err := parsePomCached(filepath.Join(repoRoot, filepath.FromSlash(rel), "pom.xml"))
		if err == nil {
			all = append(all, found{rel, pom})
		}
		return len(all) < maxScanned
	})

	isModule := map[string]bool{}
	for _, f := range all {
		for _, m := range f.pom.Modules {
			m = path.Clean(filepath.ToSlash(strings.TrimSpace(m)))
			if strings.HasSuffix(m, ".xml") { // a module may name its pom file
				m = path.Dir(m)
			}
			if m == "." || m == ".." || strings.HasPrefix(m, "../") || path.IsAbs(m) {
				continue
			}
			isModule[path.Join(f.dir, m)] = true
		}
	}

	var out []mavenProject
	for _, f := range all {
		if isModule[f.dir] || !projectDirOK(f.dir) {
			continue
		}
		out = append(out, mavenProject{
			dir:     f.dir,
			pom:     f.pom,
			modules: collectModules(filepath.Join(repoRoot, filepath.FromSlash(f.dir)), f.pom),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// projectDirOK keeps names that can't confuse the target value or the
// picker: no "::" (the separator), and nothing that isn't printable.
func projectDirOK(dir string) bool {
	if strings.Contains(dir, "::") {
		return false
	}
	for _, r := range dir {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// target is the value of the target param: project and module, "::"
// apart. The module is empty for the whole project.
func target(project, module string) string { return project + "::" + module }

func targetLabel(project, module string) string {
	switch {
	case module == "" && project == ".":
		return "(root)"
	case module == "":
		return project
	case project == ".":
		return module
	}
	return project + " › " + module
}

// allTargets lists a project as a whole and each of its modules.
func (p mavenProject) allTargets() config.OptionList {
	out := config.OptionList{{Value: target(p.dir, ""), Label: targetLabel(p.dir, "")}}
	for _, m := range p.modules {
		out = append(out, config.Option{Value: target(p.dir, m.path), Label: targetLabel(p.dir, m.path)})
	}
	return out
}

func targetParam(opts config.OptionList) config.Param {
	return config.Param{Name: "target", Type: config.ParamSelect, Options: opts, Default: opts[0].Value, Required: true}
}

// splitTarget undoes target.
func splitTarget(v string) (project, module string) {
	project, module, _ = strings.Cut(v, "::")
	return project, module
}

// mavenTargetRun builds mvn's argument list for a target: -f the
// project's pom, -pl the module, then the rest. Paths arrive as $LC_
// values and are never spliced into the text.
func mavenTargetRun(goal string, alsoMake bool) string {
	var b strings.Builder
	b.WriteString("case \"$LC_TARGET\" in *::*) ;; *) echo \"no target picked\" >&2; exit 2 ;; esac\n")
	b.WriteString("proj=${LC_TARGET%%::*}; mod=${LC_TARGET#*::}\n")
	b.WriteString("set -- -f \"$proj/pom.xml\"\n")
	b.WriteString("[ -n \"$mod\" ] && set -- \"$@\" -pl \"$mod\"\n")
	if alsoMake {
		b.WriteString("[ \"$LC_ALSO_MAKE\" = true ] && [ -n \"$mod\" ] && set -- \"$@\" -am\n")
	}
	b.WriteString("[ -n \"$LC_PROFILE\" ] && set -- \"$@\" \"-P$LC_PROFILE\"\n")
	return b.String() + "exec mvn \"$@\" " + goal
}

func mavenTargetPreview(goal string, alsoMake bool) func(map[string]string) string {
	return func(v map[string]string) string {
		proj, mod := splitTarget(v["target"])
		args := []string{"mvn", "-f", proj + "/pom.xml"}
		if mod != "" {
			args = append(args, "-pl", mod)
			if alsoMake && v["also_make"] == "true" {
				args = append(args, "-am")
			}
		}
		if v["profile"] != "" {
			args = append(args, "-P"+v["profile"])
		}
		return strings.Join(append(args, goal), " ")
	}
}

// projectsCommands builds the commands for a repo with several Maven
// projects, or with projects but no root pom.
func (mv Maven) projectsCommands(projects []mavenProject, settings map[string]string) []config.ResolvedCommand {
	// Profiles: every project's own, together. A profile a project
	// doesn't have is only a warning from mvn.
	seen := map[string]bool{}
	var profileIDs []string
	for _, p := range projects {
		for _, pr := range p.pom.Profiles {
			if pr.ID != "" && !seen[pr.ID] {
				seen[pr.ID] = true
				profileIDs = append(profileIDs, pr.ID)
			}
		}
	}
	sort.Strings(profileIDs)
	var profileParam *config.Param
	if len(profileIDs) > 0 {
		profileParam = &config.Param{Name: "profile", Type: config.ParamSelect,
			Options: config.Opts(profileIDs...), Default: settings["default_profile"]}
	}
	withProfile := func(ps ...config.Param) []config.Param {
		if profileParam != nil {
			ps = append(ps, *profileParam)
		}
		return ps
	}

	var every config.OptionList
	for _, p := range projects {
		every = append(every, p.allTargets()...)
	}
	alsoMake := config.Param{Name: "also_make", Type: config.ParamBool, Default: "true"}

	var cmds []config.ResolvedCommand
	for _, phase := range lifecyclePhases {
		cmds = append(cmds, config.ResolvedCommand{
			ID: namespacedID("maven", phase), Name: namespacedName("maven", phase),
			Params:  withProfile(targetParam(every), alsoMake),
			Run:     mavenTargetRun(phase, true),
			Preview: mavenTargetPreview(phase, true),
		})
	}

	have := map[string]bool{}
	for _, c := range cmds {
		have[c.ID] = true
	}
	for _, g := range mv.goalTable() {
		id := namespacedID("maven", g.goal)
		if have[id] {
			continue
		}
		var opts config.OptionList
		for _, p := range projects {
			atRoot := g.always || p.pom.declares(g.artifactIDs)
			switch {
			case !g.run && atRoot:
				opts = append(opts, p.allTargets()...)
			case g.run && len(p.modules) == 0 && atRoot:
				opts = append(opts, p.allTargets()...) // a single module app
			case g.run:
				// Application goals run in one module, never the whole
				// reactor: the ones that declare the plugin, or every
				// module when the project's own pom does.
				for _, m := range p.modules {
					if atRoot || m.pom.declares(g.artifactIDs) {
						opts = append(opts, config.Option{Value: target(p.dir, m.path), Label: targetLabel(p.dir, m.path)})
					}
				}
			}
		}
		if len(opts) == 0 {
			continue
		}
		if !g.run {
			cmds = append(cmds, config.ResolvedCommand{
				ID: id, Name: namespacedName("maven", g.goal),
				Params:  withProfile(targetParam(opts), alsoMake),
				Run:     mavenTargetRun(g.goal, true),
				Preview: mavenTargetPreview(g.goal, true),
			})
			continue
		}
		cmds = append(cmds, mv.projectsRunCommand(g, opts, withProfile(targetParam(opts))))
	}
	return cmds
}

// projectsRunCommand is an application goal for the multi-project form:
// the target picks the project and module, and there is no -am, since it
// would try to start every upstream module too.
func (Maven) projectsRunCommand(g pluginGoal, opts config.OptionList, params []config.Param) config.ResolvedCommand {
	run := mavenTargetRun(g.goal, false)
	if g.appProfile != "" {
		params = append(params, config.Param{Name: g.appProfile, Type: config.ParamString})
		env := "LC_" + strings.ToUpper(g.appProfile)
		run = strings.Replace(run, "exec mvn", "[ -n \"$"+env+"\" ] && set -- \"$@\" \"-D"+g.appProfileProp+"=$"+env+"\"\nexec mvn", 1)
	}
	preview := mavenTargetPreview(g.goal, false)
	return config.ResolvedCommand{
		ID: namespacedID("maven", g.goal), Name: namespacedName("maven", g.goal),
		Desc:   "Runs in one module, without -am: install the modules it depends on first (maven: install).",
		Params: params, Run: run,
		Preview: func(v map[string]string) string {
			out := preview(v)
			if g.appProfile != "" && v[g.appProfile] != "" {
				out = strings.Replace(out, " "+g.goal, " -D"+g.appProfileProp+"="+v[g.appProfile]+" "+g.goal, 1)
			}
			return out
		},
	}
}
