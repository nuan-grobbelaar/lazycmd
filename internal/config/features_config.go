package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FeaturesFile is config.yaml's shape, read from both the global config
// dir and the project's .lazycmd dir.
type FeaturesFile struct {
	// Features re-enables features a broader file disabled; every
	// feature is on by default whenever it detects its files.
	Features []string `yaml:"features,omitempty"`
	// Disable turns features off, e.g. [git] to hide git commands.
	Disable []string          `yaml:"disable,omitempty"`
	Maven   MavenSettings     `yaml:"maven,omitempty"`
	Npm     map[string]string `yaml:"npm,omitempty"`
	Vite    map[string]string `yaml:"vite,omitempty"`
	Git     map[string]string `yaml:"git,omitempty"`
}

// MavenSettings is the maven: block: string settings like
// default_profile, plus goals: a list of extra or adjusted goals.
type MavenSettings struct {
	Values map[string]string
	Goals  []MavenGoal
}

// MavenGoal teaches the maven feature about a goal. A goal that is
// already built in (spring-boot:run) gains the plugin as another way to
// detect it; any other goal is added.
//
// Goal and the other fields end up in the shell command that runs mvn,
// and config.yaml can come from a repo, so every field is checked
// against a strict pattern (validate) and anything else is dropped.
type MavenGoal struct {
	// Goal is what mvn runs: "spring-boot:run", or several separated by
	// spaces ("clean install").
	Goal string
	// Plugin is the artifactId, in <build><plugins>, that makes the goal
	// show up. Empty means show it whenever the repo has a pom.xml.
	Plugin string
	// Run says the goal starts an application: it asks for a module and
	// never uses -am. Left unset, a built-in goal keeps its own setting
	// and a new goal is not a run goal.
	Run *bool
	// ProfileParam and ProfileProp add a field to the form, passed as
	// -D<ProfileProp>=<value> (a framework's own profile setting).
	ProfileParam string
	ProfileProp  string
}

var (
	goalToken   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	mavenName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	reservedMvn = map[string]bool{"module": true, "profile": true, "also_make": true}
)

func (g MavenGoal) validate() error {
	if strings.TrimSpace(g.Goal) == "" {
		return fmt.Errorf("goal is required")
	}
	for _, tok := range strings.Split(g.Goal, " ") {
		if !goalToken.MatchString(tok) {
			return fmt.Errorf("goal %q: each goal may only use letters, digits and . _ : -, separated by single spaces", g.Goal)
		}
	}
	if g.Plugin != "" && !mavenName.MatchString(g.Plugin) {
		return fmt.Errorf("plugin %q: an artifactId may only use letters, digits and . _ -", g.Plugin)
	}
	if (g.ProfileParam == "") != (g.ProfileProp == "") {
		return fmt.Errorf("profile_param and profile_prop go together")
	}
	if g.ProfileParam != "" {
		if !paramName.MatchString(g.ProfileParam) || reservedMvn[strings.ToLower(g.ProfileParam)] {
			return fmt.Errorf("profile_param %q: use letters, digits and _, and not module, profile or also_make", g.ProfileParam)
		}
		if !mavenName.MatchString(g.ProfileProp) {
			return fmt.Errorf("profile_prop %q: use letters, digits and . _ -", g.ProfileProp)
		}
	}
	return nil
}

// UnmarshalYAML reads goals: as a list and every other key as a string
// setting. Unknown keys inside a goal are an error, like everywhere else.
func (m *MavenSettings) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: maven must be a mapping", n.Line)
	}
	m.Values = map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value != "goals" {
			var str string
			if err := v.Decode(&str); err != nil {
				return fmt.Errorf("line %d: maven.%s must be a string", v.Line, k.Value)
			}
			m.Values[k.Value] = str
			continue
		}
		if v.Kind != yaml.SequenceNode {
			return fmt.Errorf("line %d: maven.goals must be a list", v.Line)
		}
		for _, item := range v.Content {
			g, err := decodeMavenGoal(item)
			if err != nil {
				return err
			}
			m.Goals = append(m.Goals, g)
		}
	}
	return nil
}

func decodeMavenGoal(n *yaml.Node) (MavenGoal, error) {
	var g MavenGoal
	if n.Kind != yaml.MappingNode {
		return g, fmt.Errorf("line %d: each maven goal must be a mapping with a goal: key", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		var err error
		switch k.Value {
		case "goal":
			err = v.Decode(&g.Goal)
		case "plugin":
			err = v.Decode(&g.Plugin)
		case "profile_param":
			err = v.Decode(&g.ProfileParam)
		case "profile_prop":
			err = v.Decode(&g.ProfileProp)
		case "run":
			var b bool
			if err = v.Decode(&b); err == nil {
				g.Run = &b
			}
		default:
			return g, fmt.Errorf("line %d: unknown field %q in a maven goal (goal, plugin, run, profile_param, profile_prop)", k.Line, k.Value)
		}
		if err != nil {
			return g, fmt.Errorf("line %d: maven goal %s: %v", v.Line, k.Value, err)
		}
	}
	return g, nil
}

// AllFeatures lists every built-in feature. They are all enabled by
// default: detection only parses files, so a feature simply stays quiet
// when its files aren't there.
var AllFeatures = []string{"maven", "npm", "vite", "git"}

// EnabledFeatures starts with every feature on, then applies the global
// file and then the project file, each able to disable features
// (disable:) or switch them back on (features:). Per-feature settings
// from the project override the global ones key by key.
//
// extra holds settings from other sources (a central project profile);
// they apply after the global file and before the repo's own.
//
// mavenGoals are the maven.goals entries of every file, in that order,
// with invalid ones dropped (and reported in warnings).
func EnabledFeatures(globalDir, projectDir string, extra ...FeaturesFile) (enabled map[string]bool, settings map[string]map[string]string, mavenGoals []MavenGoal, warnings []string) {
	g, w1 := loadFeaturesFile(globalDir)
	p, w2 := loadFeaturesFile(projectDir)
	warnings = append(w1, w2...)
	ordered := append(append([]FeaturesFile{g}, extra...), p)

	enabled = map[string]bool{}
	for _, name := range AllFeatures {
		enabled[name] = true
	}
	for _, f := range ordered {
		for _, name := range f.Disable {
			enabled[name] = false
		}
		for _, name := range f.Features {
			enabled[name] = true
		}
	}

	pick := func(get func(FeaturesFile) map[string]string) map[string]string {
		out := map[string]string{}
		for _, f := range ordered {
			for k, v := range get(f) {
				out[k] = v
			}
		}
		return out
	}
	for _, f := range ordered {
		for _, g := range f.Maven.Goals {
			if err := g.validate(); err != nil {
				warnings = append(warnings, fmt.Sprintf("maven goal ignored: %v", err))
				continue
			}
			mavenGoals = append(mavenGoals, g)
		}
	}
	settings = map[string]map[string]string{
		"maven": pick(func(f FeaturesFile) map[string]string { return f.Maven.Values }),
		"npm":   pick(func(f FeaturesFile) map[string]string { return f.Npm }),
		"vite":  pick(func(f FeaturesFile) map[string]string { return f.Vite }),
		"git":   pick(func(f FeaturesFile) map[string]string { return f.Git }),
	}
	return enabled, settings, mavenGoals, warnings
}

func loadFeaturesFile(dir string) (FeaturesFile, []string) {
	path := filepath.Join(dir, "config.yaml")
	var f FeaturesFile
	if _, err := os.Stat(path); err != nil {
		return f, nil
	}
	if err := decodeStrict(path, &f); err != nil {
		return FeaturesFile{}, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	return f, nil
}
