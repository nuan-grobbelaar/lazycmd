package config

import (
	"path/filepath"
	"testing"
)

func TestEnabledFeaturesSettingsMerge(t *testing.T) {
	global := t.TempDir()
	writeFile(t, filepath.Join(global, "config.yaml"), `
maven:
  default_profile: dev
`)
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "config.yaml"), `
maven:
  default_profile: ci
`)

	_, settings, warnings := EnabledFeatures(global, project)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if settings["maven"]["default_profile"] != "ci" {
		t.Errorf("maven.default_profile = %q, want project override \"ci\"", settings["maven"]["default_profile"])
	}
}

func TestEnabledFeaturesDisableAndReenable(t *testing.T) {
	global := t.TempDir()
	writeFile(t, filepath.Join(global, "config.yaml"), "disable: [git, vite]\n")
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "config.yaml"), "features: [vite]\ndisable: [maven]\n")

	enabled, _, warnings := EnabledFeatures(global, project)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	want := map[string]bool{"maven": false, "npm": true, "vite": true, "git": false}
	for name, on := range want {
		if enabled[name] != on {
			t.Errorf("enabled[%q] = %v, want %v (all: %v)", name, enabled[name], on, enabled)
		}
	}
}

func TestEnabledFeaturesAllOnByDefault(t *testing.T) {
	enabled, _, warnings := EnabledFeatures(t.TempDir(), t.TempDir())
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	for _, name := range AllFeatures {
		if !enabled[name] {
			t.Errorf("%q should be on by default, got %v", name, enabled)
		}
	}
}
