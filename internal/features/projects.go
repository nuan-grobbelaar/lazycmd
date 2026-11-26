package features

import (
	"fmt"
	"path"
	"strings"

	"lazycmd/internal/config"
)

// When a feature finds more than one project of its kind (several npm
// packages, several compose files, ...), each of its commands asks which
// project to use. The feature builds each project's commands exactly as
// it does for a repo with one, and mergeProjects joins them:
//
//   - one command per name, with a required "project" select listing the
//     projects that have it
//   - the shell body picks the project's own body by $LC_PROJECT, after
//     cd-ing into it, so every project keeps its own runner and arguments
//   - a param whose choices differ between projects (a compose file's
//     services, an npm package's workspaces) is filled in by an
//     options_cmd that depends on the project
//
// Project folders come from the repo, so they only ever reach the shell
// as quoted words or as the $LC_PROJECT value.

// projectCommands is one project's commands.
type projectCommands struct {
	dir  string // relative to the repo root; "." for the root
	cmds []config.ResolvedCommand
}

// Project labels: the project's folder name, or with label "path" its
// path from the repo root. A name two projects share is shown as the path.
const (
	labelFolder = "folder"
	labelPath   = "path"
)

// labelSetting reads a feature's label setting.
func labelSetting(settings map[string]string) (string, error) {
	switch v := settings["label"]; v {
	case "", labelFolder:
		return labelFolder, nil
	case labelPath:
		return labelPath, nil
	default:
		return "", fmt.Errorf("label must be %q or %q, not %q", labelFolder, labelPath, v)
	}
}

func projectLabels(dirs []string, mode string) map[string]string {
	name := func(d string) string {
		switch {
		case d == ".":
			return "(root)"
		case mode == labelPath:
			return d
		}
		return path.Base(d)
	}
	count := map[string]int{}
	for _, d := range dirs {
		count[name(d)]++
	}
	out := map[string]string{}
	for _, d := range dirs {
		out[d] = name(d)
		if count[name(d)] > 1 {
			out[d] = d
		}
	}
	return out
}

// mergeProjects joins the projects' commands. The result is in the order
// the commands first appear.
func mergeProjects(projects []projectCommands, mode string) []config.ResolvedCommand {
	var dirs []string
	for _, p := range projects {
		dirs = append(dirs, p.dir)
	}
	labels := projectLabels(dirs, mode)

	type entry struct {
		dir string
		cmd config.ResolvedCommand
	}
	var order []string
	groups := map[string][]entry{}
	for _, p := range projects {
		for _, c := range p.cmds {
			if _, seen := groups[c.ID]; !seen {
				order = append(order, c.ID)
			}
			groups[c.ID] = append(groups[c.ID], entry{p.dir, c})
		}
	}

	var out []config.ResolvedCommand
	for _, id := range order {
		es := groups[id]
		var have []string
		var opts config.OptionList
		var per []dirParams
		for _, e := range es {
			have = append(have, e.dir)
			opts = append(opts, config.Option{Value: e.dir, Label: labels[e.dir]})
			per = append(per, dirParams{e.dir, e.cmd.Params})
		}

		merged := es[0].cmd
		merged.Params = append([]config.Param{{
			Name: "project", Type: config.ParamSelect, Options: opts, Default: have[0], Required: true,
		}}, mergeParams(per, have)...)

		var body strings.Builder
		body.WriteString("case \"$LC_PROJECT\" in\n")
		for _, e := range es {
			q := shellQuote(e.dir)
			fmt.Fprintf(&body, "%s)\ncd -- %s || exit 1\n%s\n;;\n", q, q, e.cmd.Run)
			if e.cmd.Terminal {
				merged.Terminal = true
			}
			if merged.Desc == "" {
				merged.Desc = e.cmd.Desc
			}
		}
		body.WriteString("*) echo \"no project picked\" >&2; exit 2 ;;\nesac")
		merged.Run = body.String()

		byDir := map[string]config.ResolvedCommand{}
		for _, e := range es {
			byDir[e.dir] = e.cmd
		}
		merged.Preview = func(v map[string]string) string {
			dir := v["project"]
			c, ok := byDir[dir]
			if !ok || c.Preview == nil {
				return ""
			}
			text := c.Preview(v)
			if dir == "." {
				return text
			}
			return "cd " + shellQuote(dir) + " && " + text
		}
		out = append(out, merged)
	}
	return out
}

type dirParams struct {
	dir    string
	params []config.Param
}

// mergeParams joins the projects' params by name. A select or multi whose
// choices differ between projects becomes an options_cmd that depends on
// the project; everything else is taken from the first project that has it.
func mergeParams(per []dirParams, dirs []string) []config.Param {
	var names []string
	byName := map[string]map[string]config.Param{}
	for _, dp := range per {
		for _, p := range dp.params {
			if byName[p.Name] == nil {
				byName[p.Name] = map[string]config.Param{}
				names = append(names, p.Name)
			}
			byName[p.Name][dp.dir] = p
		}
	}

	var out []config.Param
	for _, name := range names {
		perDir := byName[name]
		var first config.Param
		for _, d := range dirs {
			if p, ok := perDir[d]; ok {
				first = p
				break
			}
		}
		if first.Type != config.ParamSelect && first.Type != config.ParamMulti {
			out = append(out, first)
			continue
		}
		if len(perDir) == len(dirs) && sameOptions(perDir, dirs) {
			out = append(out, first)
			continue
		}

		// The choices depend on the project.
		var b strings.Builder
		b.WriteString("case \"$LC_PROJECT\" in\n")
		for _, d := range dirs {
			list := perDir[d].Options
			if _, ok := perDir[d]; !ok && first.Type == config.ParamSelect {
				list = config.OptionList{{Label: "(none)", Value: ""}} // a select can't be empty
			}
			var args []string
			for _, o := range list {
				if strings.ContainsAny(o.Value+o.Label, "\n\r\t") {
					continue
				}
				args = append(args, shellQuote(o.Value), shellQuote(o.Label))
			}
			if len(args) > 0 {
				fmt.Fprintf(&b, "%s) printf '%%s\\t%%s\\n' %s ;;\n", shellQuote(d), strings.Join(args, " "))
			}
		}
		b.WriteString("esac")
		first.Options = nil
		first.OptionsCmd = b.String()
		first.DependsOn = []string{"project"}
		first.Default = ""
		out = append(out, first)
	}
	return out
}

func sameOptions(perDir map[string]config.Param, dirs []string) bool {
	var ref config.OptionList
	for i, d := range dirs {
		o := perDir[d].Options
		if i == 0 {
			ref = o
			continue
		}
		if len(o) != len(ref) {
			return false
		}
		for j := range o {
			if o[j] != ref[j] {
				return false
			}
		}
	}
	return true
}
