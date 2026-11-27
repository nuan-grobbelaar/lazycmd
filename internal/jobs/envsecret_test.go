package jobs

import (
	"strings"
	"testing"

	"lazycmd/internal/config"
)

func envValue(env []string, name string) (string, bool) {
	val, found := "", false
	for _, kv := range env { // the last one wins, as for exec
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			val, found = v, true
		}
	}
	return val, found
}

func TestEmptySecretKeepsEnvironmentValue(t *testing.T) {
	t.Setenv("LC_TOKEN", "from-env")
	t.Setenv("LC_NAME", "from-env")
	cmd := config.ResolvedCommand{Name: "d", ScriptPath: "/bin/true", Params: []config.Param{
		{Name: "token", Type: config.ParamSecret},
		{Name: "name", Type: config.ParamString},
	}}

	ec, err := BuildCmd(cmd, map[string]string{"token": "", "name": ""})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(ec.Env, "LC_TOKEN"); v != "from-env" {
		t.Errorf("an empty secret should keep LC_TOKEN from the environment, got %q", v)
	}
	if v, _ := envValue(ec.Env, "LC_NAME"); v != "" {
		t.Errorf("a non-secret answer always wins, got LC_NAME=%q", v)
	}

	ec, _ = BuildCmd(cmd, map[string]string{"token": "typed", "name": "x"})
	if v, _ := envValue(ec.Env, "LC_TOKEN"); v != "typed" {
		t.Errorf("a typed secret wins over the environment, got %q", v)
	}
}
