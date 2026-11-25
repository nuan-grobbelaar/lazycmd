package features

import (
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lazycmd/internal/config"
)

type Maven struct{}

func (Maven) Name() string { return "maven" }

func (Maven) Detect(repoRoot string) (bool, string) {
	if !fileExists(filepath.Join(repoRoot, "pom.xml")) {
		return false, ""
	}
	return true, "pom.xml"
}

var lifecyclePhases = []string{"validate", "compile", "test", "package", "verify", "install", "deploy"}

// pluginGoal is a goal offered when a pom declares its plugin in
// <build><plugins> (pluginManagement alone doesn't count: it only
// configures a plugin, it doesn't apply it).
type pluginGoal struct {
	artifactID string
	goal       string

	// run marks a goal that starts an application (spring-boot:run and
	// friends). Those are usually declared in one app module rather than
	// the root, so module poms are searched too, and the module picker
	// only lists modules that declare the plugin. They never get -am:
	// with a run goal it would try to start every upstream module too.
	run bool

	// appProfile, if set, adds a string param of that name, passed as
	// -D<appProfileProp>=<value>. That's the framework's own profile
	// (Spring's, Quarkus's), separate from Maven's -P profiles.
	appProfile     string
	appProfileProp string
}

var pluginGoals = []pluginGoal{
	{artifactID: "spotless-maven-plugin", goal: "spotless:check"},
	{artifactID: "maven-checkstyle-plugin", goal: "checkstyle:check"},
	{artifactID: "spotbugs-maven-plugin", goal: "spotbugs:check"},
	{artifactID: "maven-pmd-plugin", goal: "pmd:check"},
	{artifactID: "spring-boot-maven-plugin", goal: "spring-boot:run", run: true,
		appProfile: "spring_profiles", appProfileProp: "spring-boot.run.profiles"},
	{artifactID: "quarkus-maven-plugin", goal: "quarkus:dev", run: true,
		appProfile: "quarkus_profile", appProfileProp: "quarkus.profile"},
	{artifactID: "jetty-maven-plugin", goal: "jetty:run", run: true},
}

type pomXML struct {
	Modules  []string `xml:"modules>module"`
	Profiles []struct {
		ID string `xml:"id"`
	} `xml:"profiles>profile"`
	Build struct {
		Plugins []struct {
			ArtifactID string `xml:"artifactId"`
		} `xml:"plugins>plugin"`
	} `xml:"build"`
}

func (p pomXML) declares(artifactID string) bool {
	for _, pl := range p.Build.Plugins {
		if strings.TrimSpace(pl.ArtifactID) == artifactID {
			return true
		}
	}
	return false
}

type pomCacheEntry struct {
	modTime time.Time
	parsed  pomXML
}

// pomCache holds parsed poms by path, re-read when a file's mtime
// changes, since a multi-module project parses every module pom.
var pomCache = struct {
	sync.Mutex
	m map[string]pomCacheEntry
}{m: map[string]pomCacheEntry{}}

func parsePomCached(path string) (pomXML, error) {
	info, err := os.Stat(path)
	if err != nil {
		return pomXML{}, err
	}

	pomCache.Lock()
	defer pomCache.Unlock()
	if e, ok := pomCache.m[path]; ok && e.modTime.Equal(info.ModTime()) {
		return e.parsed, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return pomXML{}, err
	}
	var p pomXML
	if err := xml.Unmarshal(data, &p); err != nil {
		return pomXML{}, err
	}
	pomCache.m[path] = pomCacheEntry{modTime: info.ModTime(), parsed: p}
	return p, nil
}

// mavenModule is a module pom found below the root, with its path
// relative to the root in the form -pl accepts ("api", "services/web").
type mavenModule struct {
	path string
	pom  pomXML
}

const (
	maxModuleDepth = 4
	maxModules     = 200
)

// collectModules walks <modules> from the root pom down, reading only
// files. Module paths come from the repo, so one that leaves the repo
// ("../x", "/etc") is ignored, as is a module whose pom is missing or
// doesn't parse: modules only add options, they never fail the feature.
func collectModules(repoRoot string, root pomXML) []mavenModule {
	type item struct {
		rel   string
		pom   pomXML
		depth int
	}
	var out []mavenModule
	seen := map[string]bool{}
	queue := []item{{rel: ".", pom: root}}
	for len(queue) > 0 && len(out) < maxModules {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxModuleDepth {
			continue
		}
		for _, m := range cur.pom.Modules {
			m = filepath.ToSlash(strings.TrimSpace(m))
			if m == "" || path.IsAbs(m) || filepath.IsAbs(m) {
				continue
			}
			rel := path.Join(cur.rel, m) // Join cleans
			if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || seen[rel] {
				continue
			}
			seen[rel] = true
			pom, err := parsePomCached(filepath.Join(repoRoot, filepath.FromSlash(rel), "pom.xml"))
			if err != nil {
				continue
			}
			out = append(out, mavenModule{path: rel, pom: pom})
			queue = append(queue, item{rel: rel, pom: pom, depth: cur.depth + 1})
			if len(out) >= maxModules {
				break
			}
		}
	}
	return out
}

func (Maven) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	pom, err := parsePomCached(filepath.Join(repoRoot, "pom.xml"))
	if err != nil {
		return nil, err
	}

	var profileParam *config.Param
	var profileIDs []string
	for _, p := range pom.Profiles {
		if p.ID != "" {
			profileIDs = append(profileIDs, p.ID)
		}
	}
	if len(profileIDs) > 0 {
		profileParam = &config.Param{
			Name: "profile", Type: config.ParamSelect,
			Options: config.Opts(profileIDs...), Default: settings["default_profile"],
		}
	}

	var params []config.Param
	if len(pom.Modules) > 0 {
		params = append(params, config.Param{Name: "module", Type: config.ParamSelect, Options: config.Opts(pom.Modules...)})
	}
	if profileParam != nil {
		params = append(params, *profileParam)
	}
	if len(pom.Modules) > 0 {
		// -am only means something together with -pl.
		params = append(params, config.Param{Name: "also_make", Type: config.ParamBool, Default: "true"})
	}

	var cmds []config.ResolvedCommand
	for _, phase := range lifecyclePhases {
		cmds = append(cmds, mavenCommand(phase, params))
	}

	var modules []mavenModule
	modulesRead := false
	for _, g := range pluginGoals {
		if !g.run {
			if pom.declares(g.artifactID) {
				cmds = append(cmds, mavenCommand(g.goal, nil))
			}
			continue
		}
		if !modulesRead {
			modules, modulesRead = collectModules(repoRoot, pom), true
		}
		if c, ok := runGoalCommand(g, pom, modules, profileParam); ok {
			cmds = append(cmds, c)
		}
	}
	return cmds, nil
}

// mavenRun builds mvn's argument list from the LC_ params, one flag per
// line of shell so an unset or false param just drops its flag.
const mavenRun = `set --
[ -n "$LC_MODULE" ] && set -- "$@" -pl "$LC_MODULE"
[ "$LC_ALSO_MAKE" = true ] && [ -n "$LC_MODULE" ] && set -- "$@" -am
[ -n "$LC_PROFILE" ] && set -- "$@" "-P$LC_PROFILE"
exec mvn "$@" `

func mavenCommand(goal string, params []config.Param) config.ResolvedCommand {
	return config.ResolvedCommand{
		ID:     namespacedID("maven", goal),
		Name:   namespacedName("maven", goal),
		Params: params,
		Run:    mavenRun + goal,
		Preview: func(v map[string]string) string {
			args := []string{"mvn", goal}
			if v["module"] != "" {
				args = append(args, "-pl", v["module"])
				if v["also_make"] == "true" {
					args = append(args, "-am")
				}
			}
			if v["profile"] != "" {
				args = append(args, "-P"+v["profile"])
			}
			return strings.Join(args, " ")
		},
	}
}

// runGoalCommand builds the command for an application goal, or reports
// false when neither the root nor any module declares its plugin.
//
// Where it runs: with no modules, at the root. With modules, a module
// must be picked: the ones that declare the plugin, or every module when
// the root declares it (children inherit <build><plugins>). Running it
// across the whole reactor would try to start each module in turn.
func runGoalCommand(g pluginGoal, root pomXML, modules []mavenModule, profileParam *config.Param) (config.ResolvedCommand, bool) {
	atRoot := root.declares(g.artifactID)
	var where []string
	for _, m := range modules {
		if atRoot || m.pom.declares(g.artifactID) {
			where = append(where, m.path)
		}
	}
	if !atRoot && len(where) == 0 {
		return config.ResolvedCommand{}, false
	}

	var params []config.Param
	if len(where) > 0 {
		params = append(params, config.Param{
			Name: "module", Type: config.ParamSelect,
			Options: config.Opts(where...), Default: where[0], Required: true,
		})
	}
	if profileParam != nil {
		params = append(params, *profileParam)
	}

	run := `set --
[ -n "$LC_MODULE" ] && set -- "$@" -pl "$LC_MODULE"
[ -n "$LC_PROFILE" ] && set -- "$@" "-P$LC_PROFILE"
`
	if g.appProfile != "" {
		params = append(params, config.Param{Name: g.appProfile, Type: config.ParamString})
		env := "LC_" + strings.ToUpper(g.appProfile)
		run += fmt.Sprintf("[ -n \"$%s\" ] && set -- \"$@\" \"-D%s=$%s\"\n", env, g.appProfileProp, env)
	}
	run += "exec mvn \"$@\" " + g.goal

	desc := ""
	if len(where) > 0 {
		desc = "Runs in one module, without -am: install the modules it depends on first (maven: install)."
	}

	return config.ResolvedCommand{
		ID:     namespacedID("maven", g.goal),
		Name:   namespacedName("maven", g.goal),
		Desc:   desc,
		Params: params,
		Run:    run,
		Preview: func(v map[string]string) string {
			args := []string{"mvn", g.goal}
			if v["module"] != "" {
				args = append(args, "-pl", v["module"])
			}
			if v["profile"] != "" {
				args = append(args, "-P"+v["profile"])
			}
			if g.appProfile != "" && v[g.appProfile] != "" {
				args = append(args, "-D"+g.appProfileProp+"="+v[g.appProfile])
			}
			return strings.Join(args, " ")
		},
	}, true
}
