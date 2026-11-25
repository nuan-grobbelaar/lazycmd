package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// FeaturesFile is config.yaml's shape, read from both the global config
// dir and the project's .lazycmd dir.
type FeaturesFile struct {
	// Features re-enables features a broader file disabled; every
	// feature is on by default whenever it detects its files.
	Features []string `yaml:"features,omitempty"`
	// Disable turns features off, e.g. [git] to hide git commands.
	Disable []string          `yaml:"disable,omitempty"`
	Maven   map[string]string `yaml:"maven,omitempty"`
	Npm     map[string]string `yaml:"npm,omitempty"`
	Vite    map[string]string `yaml:"vite,omitempty"`
	Git     map[string]string `yaml:"git,omitempty"`
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
func EnabledFeatures(globalDir, projectDir string, extra ...FeaturesFile) (enabled map[string]bool, settings map[string]map[string]string, warnings []string) {
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
	settings = map[string]map[string]string{
		"maven": pick(func(f FeaturesFile) map[string]string { return f.Maven }),
		"npm":   pick(func(f FeaturesFile) map[string]string { return f.Npm }),
		"vite":  pick(func(f FeaturesFile) map[string]string { return f.Vite }),
		"git":   pick(func(f FeaturesFile) map[string]string { return f.Git }),
	}
	return enabled, settings, warnings
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
