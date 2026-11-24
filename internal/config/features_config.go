package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// FeaturesFile is config.yaml's shape, read from both the global config
// dir and the project's .lazycast dir.
type FeaturesFile struct {
	Features []string          `yaml:"features,omitempty"`
	Maven    map[string]string `yaml:"maven,omitempty"`
	Npm      map[string]string `yaml:"npm,omitempty"`
	Vite     map[string]string `yaml:"vite,omitempty"`
	Git      map[string]string `yaml:"git,omitempty"`
}

// EnabledFeatures merges config.yaml from globalDir and projectDir: the
// project's enabled set adds to the global one, and its per-feature
// settings override the global ones key by key.
func EnabledFeatures(globalDir, projectDir string) (enabled map[string]bool, settings map[string]map[string]string, warnings []string) {
	g, w1 := loadFeaturesFile(globalDir)
	p, w2 := loadFeaturesFile(projectDir)
	warnings = append(w1, w2...)

	enabled = map[string]bool{}
	for _, name := range g.Features {
		enabled[name] = true
	}
	for _, name := range p.Features {
		enabled[name] = true
	}

	settings = map[string]map[string]string{
		"maven": mergeSettings(g.Maven, p.Maven),
		"npm":   mergeSettings(g.Npm, p.Npm),
		"vite":  mergeSettings(g.Vite, p.Vite),
		"git":   mergeSettings(g.Git, p.Git),
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

func mergeSettings(global, project map[string]string) map[string]string {
	out := make(map[string]string, len(global)+len(project))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range project {
		out[k] = v
	}
	return out
}
