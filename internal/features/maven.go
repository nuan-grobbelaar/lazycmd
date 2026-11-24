package features

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lazycast/internal/config"
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

// linterGoals maps a plugin's artifactId, as declared in <build><plugins>,
// to the goal that runs it standalone.
var linterGoals = map[string]string{
	"spotless-maven-plugin":   "spotless:check",
	"maven-checkstyle-plugin": "checkstyle:check",
	"spotbugs-maven-plugin":   "spotbugs:check",
	"maven-pmd-plugin":        "pmd:check",
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

var pomCache struct {
	sync.Mutex
	path    string
	modTime time.Time
	parsed  pomXML
}

func parsePomCached(path string) (pomXML, error) {
	info, err := os.Stat(path)
	if err != nil {
		return pomXML{}, err
	}

	pomCache.Lock()
	defer pomCache.Unlock()
	if pomCache.path == path && pomCache.modTime.Equal(info.ModTime()) {
		return pomCache.parsed, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return pomXML{}, err
	}
	var p pomXML
	if err := xml.Unmarshal(data, &p); err != nil {
		return pomXML{}, err
	}

	pomCache.path = path
	pomCache.modTime = info.ModTime()
	pomCache.parsed = p
	return p, nil
}

func (Maven) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	pom, err := parsePomCached(filepath.Join(repoRoot, "pom.xml"))
	if err != nil {
		return nil, err
	}

	var params []config.Param
	if len(pom.Modules) > 0 {
		params = append(params, config.Param{Name: "module", Type: config.ParamSelect, Options: pom.Modules})
	}
	var profileIDs []string
	for _, p := range pom.Profiles {
		if p.ID != "" {
			profileIDs = append(profileIDs, p.ID)
		}
	}
	if len(profileIDs) > 0 {
		params = append(params, config.Param{
			Name: "profile", Type: config.ParamSelect,
			Options: profileIDs, Default: settings["default_profile"],
		})
	}

	var cmds []config.ResolvedCommand
	for _, phase := range lifecyclePhases {
		cmds = append(cmds, mavenCommand(phase, params))
	}
	for _, plugin := range pom.Build.Plugins {
		if goal, ok := linterGoals[plugin.ArtifactID]; ok {
			cmds = append(cmds, mavenCommand(goal, nil))
		}
	}
	return cmds, nil
}

func mavenCommand(goal string, params []config.Param) config.ResolvedCommand {
	return config.ResolvedCommand{
		ID:     namespacedID("maven", goal),
		Name:   namespacedName("maven", goal),
		Mode:   config.ModePanel,
		Params: params,
		Run:    `mvn ${LC_MODULE:+-pl "$LC_MODULE"} ${LC_PROFILE:+-P"$LC_PROFILE"} ` + goal,
	}
}
