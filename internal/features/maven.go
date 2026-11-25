package features

import (
	"encoding/xml"
	"os"
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
	if len(pom.Modules) > 0 {
		// -am only means something together with -pl.
		params = append(params, config.Param{Name: "also_make", Type: config.ParamBool, Default: "true"})
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
