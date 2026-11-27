package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProfileFile is the shape of ~/.config/lazycmd/projects/<name>.yaml: a
// central, per-repo profile kept outside the repo, so nothing needs to
// be committed to a project to tune how lazycmd treats it.
//
//	path: ~/repos/vandag          # which repo (or paths: [...]); default: file name = repo folder name
//	include:                      # extra commands for "This project"
//	  - ~/shared/db/migrate.lazycmd.yaml   # sidecar (its script sits beside it)
//	  - ~/shared/tools/commands.yaml       # a commands.yaml-shaped file
//	  - ~/shared/scripts                   # a folder of scripts
//	  - ./deploy.sh                        # a script (+ sidecar beside it)
//	defaults:                     # preselected values in param forms
//	  deploy-staging: { env: staging }
//	  "maven: test": { module: api }
//	terminal: [pick-repo]         # commands to run with the real terminal on enter
//	params:                       # this repo's choices for select/multi params
//	  echo-params: { features: { options: [a, b], default: [a] } }
//	sidecars:                     # forms for scripts that live in the repo
//	  scripts/deploy.sh:          #   path from the repo root
//	    params: [{ name: env, type: select, options: [staging, prod] }]
//	  bin/release: ./vandag/release.lazycmd.yaml   # or a sidecar file
//	disable: [git]                # plus features:/maven:/npm:/... as in config.yaml
type ProfileFile struct {
	Path         string                              `yaml:"path,omitempty"`
	Paths        []string                            `yaml:"paths,omitempty"`
	Include      []string                            `yaml:"include,omitempty"`
	Sidecars     map[string]SidecarRef               `yaml:"sidecars,omitempty"`
	Defaults     map[string]map[string]any           `yaml:"defaults,omitempty"`
	Params       map[string]map[string]ParamOverride `yaml:"params,omitempty"`
	Terminal     []string                            `yaml:"terminal,omitempty"`
	Confirm      map[string]any                      `yaml:"confirm,omitempty"`
	FeaturesFile `yaml:",inline"`
}

// Profile is a loaded, matched project profile.
type Profile struct {
	File   ProfileFile
	Source string // the yaml file it came from
}

// ProfilesDir is where profiles live inside the global config dir.
func ProfilesDir(globalDir string) string { return filepath.Join(globalDir, "projects") }

// LoadProfile finds the profile whose path covers repoRoot (the repo
// itself or any folder below a listed path), or, for a profile with no
// path, whose file name equals the repo folder's name. Files are tried
// in name order and the first match wins. A file that fails to parse is
// reported and skipped. Returns nil when no profile matches.
func LoadProfile(globalDir, repoRoot string) (*Profile, []string) {
	dir := ProfilesDir(globalDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	root := resolveDir(repoRoot)

	var warnings []string
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		var f ProfileFile
		if err := decodeStrict(path, &f); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}

		paths := append([]string{}, f.Paths...)
		if f.Path != "" {
			paths = append(paths, f.Path)
		}
		matched := false
		for _, p := range paths {
			if covers(resolveDir(expandPath(p, dir)), root) {
				matched = true
				break
			}
		}
		if len(paths) == 0 && strings.TrimSuffix(e.Name(), ext) == filepath.Base(root) {
			matched = true
		}
		if matched {
			return &Profile{File: f, Source: path}, warnings
		}
	}
	return nil, warnings
}

// covers reports whether dir is root or an ancestor of root.
func covers(dir, root string) bool {
	rel, err := filepath.Rel(dir, root)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// expandPath resolves ~ and, for relative paths, anchors them at base.
func expandPath(p, base string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// resolveDir cleans p and follows symlinks when it can, so a repo
// reached through a symlink still matches its profile.
func resolveDir(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Includes loads the commands the profile's include: entries point at,
// and the repo scripts its sidecars: entries describe (repoRoot is the
// repo the profile matched). A missing or invalid entry is a warning;
// the others still load.
func (p *Profile) Includes(repoRoot string) ([]ResolvedCommand, []string) {
	base := filepath.Dir(p.Source)
	var cmds []ResolvedCommand
	var warnings []string
	for _, entry := range p.File.Include {
		path := expandPath(entry, base)
		got, w := loadInclude(path)
		cmds = append(cmds, got...)
		for _, msg := range w {
			warnings = append(warnings, fmt.Sprintf("%s (include %s): %s", p.Source, entry, msg))
		}
	}
	for _, key := range sortedKeys(p.File.Sidecars) {
		cmd, err := p.repoSidecar(repoRoot, key)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s (sidecars %s): %v", p.Source, key, err))
			continue
		}
		cmds = append(cmds, cmd)
	}
	return cmds, warnings
}

// SidecarRef is one sidecars: entry: the spec written inline, or the
// path of a .lazycmd.yaml file (~ expanded, relative to the profile).
type SidecarRef struct {
	File string
	Spec *Command
}

// UnmarshalYAML takes a string (a file) or a mapping (the spec inline),
// decoding the mapping as strictly as a sidecar file: an unknown key is
// an error, not silently ignored.
func (r *SidecarRef) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Decode(&r.File)
	case yaml.MappingNode:
		data, err := yaml.Marshal(n)
		if err != nil {
			return err
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		var spec Command
		if err := dec.Decode(&spec); err != nil {
			return fmt.Errorf("line %d: %v", n.Line, err)
		}
		r.Spec = &spec
		return nil
	default:
		return fmt.Errorf("line %d: a sidecars entry is a sidecar file's path or the spec itself", n.Line)
	}
}

// SidecarFiles lists the sidecar files the profile's sidecars: entries
// name, for reloading when one changes.
func (p *Profile) SidecarFiles() []string {
	var out []string
	for _, key := range sortedKeys(p.File.Sidecars) {
		if f := p.File.Sidecars[key].File; f != "" {
			out = append(out, expandPath(f, filepath.Dir(p.Source)))
		}
	}
	return out
}

// RepoSidecar finds the sidecars: entry for the script at path, a path
// inside repoRoot. ok is false when the profile has none for it.
func (p *Profile) RepoSidecar(repoRoot, path string) (cmd ResolvedCommand, ok bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return cmd, false, err
	}
	for _, key := range sortedKeys(p.File.Sidecars) {
		if filepath.Join(repoRoot, filepath.FromSlash(key)) == abs {
			cmd, err = p.repoSidecar(repoRoot, key)
			return cmd, true, err
		}
	}
	return cmd, false, nil
}

// repoSidecar builds the command for one sidecars: entry: the script at
// key (a path from the repo root, which must stay inside the repo) with
// the entry's spec. The entry describes that script, so it can't set
// run: or script: of its own.
func (p *Profile) repoSidecar(repoRoot, key string) (ResolvedCommand, error) {
	var cmd ResolvedCommand
	rel := filepath.FromSlash(key)
	if !filepath.IsLocal(rel) {
		return cmd, fmt.Errorf("must be a path inside the repo, from its root (like scripts/deploy.sh)")
	}
	script := filepath.Join(repoRoot, rel)
	info, err := os.Stat(script)
	switch {
	case err != nil:
		return cmd, fmt.Errorf("no such script in the repo")
	case !info.Mode().IsRegular():
		return cmd, fmt.Errorf("not a file")
	case info.Mode()&0o111 == 0:
		return cmd, fmt.Errorf("not executable (chmod +x it)")
	}

	ref := p.File.Sidecars[key]
	spec := ref.Spec
	where := "inline"
	if spec == nil {
		if ref.File == "" {
			return cmd, fmt.Errorf("empty entry: give the spec or a sidecar file")
		}
		path := expandPath(ref.File, filepath.Dir(p.Source))
		var s Command
		if err := decodeStrict(path, &s); err != nil {
			return cmd, fmt.Errorf("%s: %v", path, err)
		}
		spec, where = &s, path
	}
	if spec.Run != "" || spec.Script != "" {
		return cmd, fmt.Errorf("%s: drop run:/script:; the entry describes %s", where, key)
	}

	name := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	cmd = ResolvedCommand{ID: "project:" + name, Name: name, ScriptPath: script}
	applySpec(&cmd, *spec)
	if err := validateCommand(cmd); err != nil {
		return ResolvedCommand{}, fmt.Errorf("%s: %v", where, err)
	}
	return cmd, nil
}

func loadInclude(path string) ([]ResolvedCommand, []string) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, []string{"not found"}
	}
	dir, file := filepath.Split(path)
	dir = filepath.Clean(dir)

	switch {
	case info.IsDir():
		cmds, w := scanScripts(path, "project", nil)
		return cmds, w

	case strings.HasSuffix(file, sidecarSuffix):
		return loadSidecarInclude(dir, strings.TrimSuffix(file, sidecarSuffix))

	case strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml"):
		return loadCommandsFileAt(path)

	default:
		if info.Mode()&0o111 == 0 {
			return nil, []string{"not executable"}
		}
		cmd, err := scriptCommand(dir, file, "project")
		if err != nil {
			return nil, []string{err.Error()}
		}
		return []ResolvedCommand{cmd}, nil
	}
}

// loadSidecarInclude loads <dir>/<name>.lazycmd.yaml. The sidecar may
// carry its own run: or script:; otherwise the script of the same name
// beside it is used.
func loadSidecarInclude(dir, name string) ([]ResolvedCommand, []string) {
	sidecar := filepath.Join(dir, name+sidecarSuffix)
	var spec Command
	if err := decodeStrict(sidecar, &spec); err != nil {
		return nil, []string{err.Error()}
	}
	cmd := ResolvedCommand{ID: "project:" + name, Name: name}
	applySpec(&cmd, spec)

	switch {
	case spec.Run != "" && spec.Script != "":
		return nil, []string{"sidecar sets both run and script"}
	case spec.Run != "":
		// inline body: nothing more to find
	case spec.Script != "":
		cmd.ScriptPath = expandPath(spec.Script, dir)
	default:
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if !e.IsDir() && stem == name && !strings.HasSuffix(e.Name(), sidecarSuffix) {
				cmd.ScriptPath = filepath.Join(dir, e.Name())
				break
			}
		}
		if cmd.ScriptPath == "" {
			return nil, []string{"no script named " + name + " beside it, and the sidecar has no run: or script:"}
		}
	}
	if err := validateCommand(cmd); err != nil {
		return nil, []string{sidecar + ": " + err.Error()}
	}
	return []ResolvedCommand{cmd}, nil
}

// ApplyDefaults sets each param's Default from the profile's defaults:
// map. A key matches a command by display name ("maven: test") or id
// ("maven:test"). Problems (unknown command or param, a select value not
// among its options, a value of the wrong shape) are warnings and that
// one default is skipped. Param slices are copied before changing, since
// features share one slice across many commands.
func ApplyDefaults(groups []Group, defaults map[string]map[string]any) []string {
	if len(defaults) == 0 {
		return nil
	}
	var warnings []string
	used := map[string]bool{}

	for gi := range groups {
		for ci := range groups[gi].Commands {
			cmd := &groups[gi].Commands[ci]
			for key, values := range defaults {
				if key != cmd.Name && key != cmd.ID {
					continue
				}
				used[key] = true
				cmd.Params = append([]Param(nil), cmd.Params...)
				set := map[string]bool{}
				for _, name := range sortedKeys(values) {
					w := setDefault(cmd, name, values[name])
					warnings = append(warnings, w...)
					if len(w) == 0 {
						set[name] = true
					}
				}
				warnings = append(warnings, expandRefs(cmd, set)...)
			}
		}
	}
	for _, key := range sortedKeys(defaults) {
		if !used[key] {
			warnings = append(warnings, fmt.Sprintf("defaults: no command named %q", key))
		}
	}
	return warnings
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func setDefault(cmd *ResolvedCommand, param string, raw any) []string {
	for i := range cmd.Params {
		p := &cmd.Params[i]
		if p.Name != param {
			continue
		}
		val, err := defaultString(raw, p.Type)
		if err != nil {
			return []string{fmt.Sprintf("defaults: %s.%s: %v", cmd.Name, param, err)}
		}
		if bad := notAnOption(*p, val); bad != "" {
			return []string{fmt.Sprintf("defaults: %s.%s: %q is not one of %s", cmd.Name, param, bad, strings.Join(p.Options.Values(), ", "))}
		}
		p.Default = val
		return nil
	}
	return []string{fmt.Sprintf("defaults: %s has no param %q", cmd.Name, param)}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// defaultString renders a YAML value as the string form Param.Default
// holds: scalars as written, bools as true/false, and a list (for a
// multi param) joined with commas.
func defaultString(raw any, t ParamType) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case []any:
		if t != ParamMulti {
			return "", fmt.Errorf("a list only makes sense for a multi param")
		}
		parts := make([]string, len(v))
		for i, item := range v {
			s, err := defaultString(item, ParamString)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), nil
	default:
		return "", fmt.Errorf("unsupported value %v", raw)
	}
}

var refPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandRefs resolves ${param} references inside the defaults a profile
// just set on cmd, using the other params' defaults, so a profile can say
//
//	deploy: { env: staging, service: "${env}-api" }
//
// References resolve once, when the form opens: they read the other
// param's default, not what the user later types into it. They chain
// (a refers to b refers to c); a cycle or a reference to an unknown
// param is a warning and leaves that default's text as written. Only
// values the profile set are expanded, never a command's own default:.
func expandRefs(cmd *ResolvedCommand, set map[string]bool) []string {
	index := map[string]int{}
	for i, p := range cmd.Params {
		index[p.Name] = i
	}

	var warnings []string
	done := map[string]bool{}
	visiting := map[string]bool{}
	var resolve func(name string) bool
	resolve = func(name string) bool {
		if done[name] || !set[name] {
			return true
		}
		if visiting[name] {
			return false
		}
		visiting[name] = true
		defer delete(visiting, name)

		i := index[name]
		ok := true
		out := refPattern.ReplaceAllStringFunc(cmd.Params[i].Default, func(m string) string {
			ref := refPattern.FindStringSubmatch(m)[1]
			j, known := index[ref]
			switch {
			case !known:
				warnings = append(warnings, fmt.Sprintf("defaults: %s.%s refers to unknown param %q", cmd.Name, name, ref))
				ok = false
				return m
			case !resolve(ref):
				warnings = append(warnings, fmt.Sprintf("defaults: %s.%s: circular reference through %q", cmd.Name, name, ref))
				ok = false
				return m
			}
			return cmd.Params[j].Default
		})
		if ok {
			cmd.Params[i].Default = out
		}
		done[name] = true
		return ok
	}
	for _, name := range sortedKeys(set) {
		resolve(name)
	}
	return warnings
}
