package config

import (
	"path/filepath"
	"testing"
)

func TestEnabledFeaturesMergesGlobalAndProject(t *testing.T) {
	global := t.TempDir()
	writeFile(t, filepath.Join(global, "config.yaml"), `
features: [maven, git]
maven:
  default_profile: dev
`)
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "config.yaml"), `
features: [npm]
maven:
  default_profile: ci
`)

	enabled, settings, warnings := EnabledFeatures(global, project)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	for _, name := range []string{"maven", "git", "npm"} {
		if !enabled[name] {
			t.Errorf("expected %q enabled, got %v", name, enabled)
		}
	}
	if enabled["vite"] {
		t.Error("vite should not be enabled")
	}
	if settings["maven"]["default_profile"] != "ci" {
		t.Errorf("maven.default_profile = %q, want project override \"ci\"", settings["maven"]["default_profile"])
	}
}

func TestEnabledFeaturesNoFilesIsFine(t *testing.T) {
	enabled, _, warnings := EnabledFeatures(t.TempDir(), t.TempDir())
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(enabled) != 0 {
		t.Errorf("expected no enabled features, got %v", enabled)
	}
}
