package config

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The files in examples/ are documentation; this keeps them loading
// cleanly as the formats change.
func TestExamplesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "examples")

	all, warnings := scanScripts(filepath.Join(dir, "scripts"), "global", nil)
	if len(warnings) != 0 {
		t.Fatalf("scripts: warnings = %v", warnings)
	}
	byName := map[string]ResolvedCommand{}
	for _, c := range all {
		byName[c.Name] = c
	}
	for _, want := range []string{"deploy", "ask-demo", "pick-file", "panel-size", "broken-options"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("example %q missing; got %v", want, all)
		}
	}
	if !byName["pick-file"].Terminal || byName["deploy"].Confirm == "" || byName["broken-options"].Params[1].OptionsCmd == "" {
		t.Error("examples lost the features they demonstrate")
	}

	// The repo's own .lazycmd/ lists the same scripts, through a symlink.
	repoCmds, repoWarnings := ProjectCommands(filepath.Join("..", "..", ".lazycmd"))
	if len(repoWarnings) != 0 || len(repoCmds) != len(all) {
		t.Errorf(".lazycmd: %d commands (warnings %v), want %d", len(repoCmds), repoWarnings, len(all))
	}

	cmds := []ResolvedCommand{byName["deploy"]}

	var prof ProfileFile
	if err := decodeStrict(filepath.Join(dir, "projects", "my-app.yaml"), &prof); err != nil {
		t.Fatalf("profile: %v", err)
	}
	groups := []Group{{Title: "Global", Commands: cmds}}
	w := ApplyParamOverrides(groups, prof.Params)
	w = append(w, ApplyDefaults(groups, prof.Defaults)...)
	if len(w) != 0 {
		t.Fatalf("profile warnings: %v", w)
	}

	got := map[string]Param{}
	for _, p := range groups[0].Commands[0].Params {
		got[p.Name] = p
	}
	if s := got["services"]; strings.Join(s.Options.Values(), ",") != "api,billing,notifications" || s.Default != "api,billing" {
		t.Errorf("services = %+v", s)
	}
	if r := got["region"]; r.Default != "eu-central-1" || r.Options[1] != (Option{Label: "Cape Town", Value: "af-south-1"}) {
		t.Errorf("region = %+v", r)
	}
	if got["env"].Default != "staging" || got["tag"].Default != "staging-candidate" {
		t.Errorf("env/tag defaults = %q / %q", got["env"].Default, got["tag"].Default)
	}

	// And the script runs with what the form would send it.
	cmd := cmds[0]
	values := map[string]string{"env": "staging", "services": "api,billing", "tag": "", "dry_run": "true", "token": "s3cret"}
	c := exec.Command(cmd.ScriptPath, ExpandArgs(cmd.Args, values)...)
	c.Env = []string{"PATH=/usr/bin:/bin", "LC_SERVICES=api,billing", "LC_TOKEN=s3cret", "LC_REGION=af-south-1"}
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("deploy.sh: %v\n%s", err, out)
	}
	for _, want := range []string{"env:      staging", "region:   af-south-1", "tag:      latest", "token:    set (6 chars)", "would deploy api:latest to staging", "would deploy billing:latest to staging"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "s3cret") {
		t.Error("the script printed the token")
	}
}
