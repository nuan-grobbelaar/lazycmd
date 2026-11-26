package features

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"lazycmd/internal/config"
)

// Compose is the docker compose feature. Like the others it only reads
// files: the compose file's service names and profiles. Nothing is run
// to find them out.
type Compose struct{}

func (Compose) Name() string { return "compose" }

// composeFiles are the names Docker Compose itself looks for, in its
// order of preference.
var composeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

func (Compose) Detect(repoRoot string) (bool, string) {
	if name := composeFileIn(repoRoot); name != "" {
		return true, name
	}
	if len(projectDirs(repoRoot, composeFiles...)) > 0 {
		return true, "compose files"
	}
	return false, ""
}

// composeFileIn is the compose file in dir, by Docker's order of
// preference, or "".
func composeFileIn(dir string) string {
	for _, name := range composeFiles {
		if fileExists(filepath.Join(dir, name)) {
			return name
		}
	}
	return ""
}

// composeCLIs are the programs compose.command may select. The setting
// ends up in a shell command, and config.yaml can come from a repo, so
// it has to be one of these.
var composeCLIs = []string{"docker compose", "docker-compose", "podman compose"}

// composeName is what a service or profile may be called. The compose
// spec allows letters, digits, "." "_" and "-"; anything else is skipped
// (the name becomes a command-line argument).
var composeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const maxComposeServices = 200

type composeFile struct {
	Services map[string]struct {
		Profiles []string `yaml:"profiles"`
	} `yaml:"services"`
}

func (Compose) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	cli := "docker compose"
	if v := settings["command"]; v != "" {
		cli = v
	}
	if !contains(composeCLIs, cli) {
		return nil, fmt.Errorf("compose.command must be one of %s, not %q", strings.Join(composeCLIs, ", "), cli)
	}
	label, err := labelSetting(settings)
	if err != nil {
		return nil, fmt.Errorf("compose.%v", err)
	}

	// One compose file at the root: the plain form. Several, or none at
	// the root: each command asks which file (project) to use.
	dirs := projectDirs(repoRoot, composeFiles...)
	if len(dirs) == 0 {
		return nil, nil
	}
	if len(dirs) == 1 && dirs[0] == "." {
		return composeIn(repoRoot, cli)
	}
	var projects []projectCommands
	for _, d := range dirs {
		if cmds, err := composeIn(filepath.Join(repoRoot, filepath.FromSlash(d)), cli); err == nil && len(cmds) > 0 {
			projects = append(projects, projectCommands{d, cmds})
		}
	}
	return mergeProjects(projects, label), nil
}

// composeIn builds the commands for the compose file in dir.
func composeIn(dir, cli string) ([]config.ResolvedCommand, error) {
	file := composeFileIn(dir)
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return nil, err
	}
	var cf composeFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}

	var services []string
	profileSet := map[string]bool{}
	for name, svc := range cf.Services {
		if !composeName.MatchString(name) {
			continue
		}
		services = append(services, name)
		for _, p := range svc.Profiles {
			if composeName.MatchString(p) {
				profileSet[p] = true
			}
		}
	}
	sort.Strings(services)
	if len(services) > maxComposeServices {
		services = services[:maxComposeServices]
	}
	if len(services) == 0 {
		return nil, nil
	}
	var profiles []string
	for p := range profileSet {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)

	pickServices := config.Param{Name: "services", Type: config.ParamMulti, Options: config.Opts(services...)}
	pickOne := config.Param{Name: "service", Type: config.ParamSelect, Options: config.Opts(services...)}
	flag := func(name string, def bool) config.Param {
		d := ""
		if def {
			d = "true"
		}
		return config.Param{Name: name, Type: config.ParamBool, Default: d}
	}

	specs := []composeSpec{
		{sub: "up", params: []config.Param{pickServices, flag("detach", true), flag("build", false)},
			flags: []composeFlag{{"detach", "-d"}, {"build", "--build"}}, services: "services", profile: len(profiles) > 0},
		{sub: "down", params: []config.Param{flag("volumes", false)}, flags: []composeFlag{{"volumes", "-v"}}},
		{sub: "ps"},
		{sub: "logs", params: []config.Param{withNone(pickOne, "(all services)"), flag("follow", false)},
			flags: []composeFlag{{"follow", "-f"}}, services: "service"},
		{sub: "build", params: []config.Param{pickServices, flag("no_cache", false)},
			flags: []composeFlag{{"no_cache", "--no-cache"}}, services: "services"},
		{sub: "restart", params: []config.Param{pickServices}, services: "services"},
		{sub: "pull", params: []config.Param{pickServices}, services: "services"},
	}

	var cmds []config.ResolvedCommand
	for _, s := range specs {
		cmds = append(cmds, s.command(cli, profiles))
	}
	cmds = append(cmds, composeExec(cli, pickOne))
	return cmds, nil
}

// withNone puts an explicit empty choice first, so a pick can mean "no
// value" (all services, no profile). A select always has something
// chosen, so without it the first option would be taken.
func withNone(p config.Param, label string) config.Param {
	opts := append(config.OptionList{{Label: label, Value: ""}}, p.Options...)
	p.Options = opts
	return p
}

// composeFlag adds Flag when the bool param Param is true.
type composeFlag struct{ param, flag string }

// composeSpec describes one compose subcommand: its form and how the
// answers become arguments.
type composeSpec struct {
	sub    string
	params []config.Param
	flags  []composeFlag
	// services names the param whose value is appended as service names:
	// "services" is a multi (comma-joined), "service" a single pick.
	services string
	// profile adds a profile pick, passed as --profile before the
	// subcommand.
	profile bool
}

func (s composeSpec) command(cli string, profiles []string) config.ResolvedCommand {
	params := append([]config.Param(nil), s.params...)
	if s.profile {
		params = append(params, withNone(config.Param{Name: "profile", Type: config.ParamSelect, Options: config.Opts(profiles...)}, "(none)"))
	}
	var b strings.Builder
	b.WriteString("set --\n")
	if s.profile {
		b.WriteString("[ -n \"$LC_PROFILE\" ] && set -- \"$@\" --profile \"$LC_PROFILE\"\n")
	}
	fmt.Fprintf(&b, "set -- \"$@\" %s\n", s.sub)
	for _, f := range s.flags {
		fmt.Fprintf(&b, "[ \"$LC_%s\" = true ] && set -- \"$@\" %s\n", strings.ToUpper(f.param), f.flag)
	}
	switch s.services {
	case "services":
		// A multi value is comma-joined; split it into one argument per
		// service without letting the shell expand anything.
		b.WriteString("if [ -n \"$LC_SERVICES\" ]; then\n  oldifs=$IFS; IFS=,; set -f\n  set -- \"$@\" $LC_SERVICES\n  set +f; IFS=$oldifs\nfi\n")
	case "service":
		b.WriteString("[ -n \"$LC_SERVICE\" ] && set -- \"$@\" \"$LC_SERVICE\"\n")
	}
	fmt.Fprintf(&b, "exec %s \"$@\"", cli)

	return config.ResolvedCommand{
		ID:     namespacedID("compose", s.sub),
		Name:   namespacedName("compose", s.sub),
		Params: params,
		Run:    b.String(),
		Preview: func(v map[string]string) string {
			args := strings.Fields(cli)
			if s.profile && v["profile"] != "" {
				args = append(args, "--profile", v["profile"])
			}
			args = append(args, s.sub)
			for _, f := range s.flags {
				if v[f.param] == "true" {
					args = append(args, f.flag)
				}
			}
			switch s.services {
			case "services":
				if v["services"] != "" {
					args = append(args, strings.Split(v["services"], ",")...)
				}
			case "service":
				if v["service"] != "" {
					args = append(args, v["service"])
				}
			}
			return strings.Join(args, " ")
		},
	}
}

// composeExec runs a command in a service's container. It is interactive,
// so it takes the real terminal (terminal: true). The command goes to the
// container's own shell as one string (sh -c), so quotes and pipes work
// the way they do at a prompt, and the default "sh" opens a shell.
func composeExec(cli string, service config.Param) config.ResolvedCommand {
	service.Required = true
	return config.ResolvedCommand{
		ID:       namespacedID("compose", "exec"),
		Name:     namespacedName("compose", "exec"),
		Terminal: true,
		Params: []config.Param{
			service,
			{Name: "command", Type: config.ParamString, Default: "sh"},
		},
		Run: fmt.Sprintf("exec %s exec \"$LC_SERVICE\" sh -c \"${LC_COMMAND:-sh}\"", cli),
		Preview: func(v map[string]string) string {
			cmd := v["command"]
			if cmd == "" {
				cmd = "sh"
			}
			return strings.Join(strings.Fields(cli), " ") + " exec " + v["service"] + " sh -c " + shellQuote(cmd)
		},
	}
}
