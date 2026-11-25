package config

import (
	"fmt"
	"strings"
)

// ParamOverride is one entry of a profile's params: map. It replaces a
// param's choices and/or its default for one repo, without touching the
// command's own file (a global script's sidecar, say):
//
//	params:
//	  echo-params:
//	    features: { options: [logging, metrics, audit], default: [logging, audit] }
//
// Options apply to select and multi params, and replace options_cmd if
// the param had one. A profile can't add an options_cmd: that would make
// profiles a second place where commands get run.
type ParamOverride struct {
	Options []string `yaml:"options,omitempty"`
	Default any      `yaml:"default,omitempty"`
}

// ApplyParamOverrides applies a profile's params: map. Keys match a
// command by display name or id, as in defaults:. Problems (unknown
// command or param, options on a param that can't take them, a default
// outside the options) are warnings and skip just that part. Param slices
// are copied before changing, since features share one slice across many
// commands. Run it before ApplyDefaults, so a defaults: entry for the
// same param wins and is checked against the new options.
func ApplyParamOverrides(groups []Group, overrides map[string]map[string]ParamOverride) []string {
	if len(overrides) == 0 {
		return nil
	}
	var warnings []string
	used := map[string]bool{}

	for gi := range groups {
		for ci := range groups[gi].Commands {
			cmd := &groups[gi].Commands[ci]
			for key, params := range overrides {
				if key != cmd.Name && key != cmd.ID {
					continue
				}
				used[key] = true
				cmd.Params = append([]Param(nil), cmd.Params...)
				set := map[string]bool{}
				for _, name := range sortedKeys(params) {
					defaultSet, w := overrideParam(cmd, name, params[name])
					warnings = append(warnings, w...)
					if defaultSet {
						set[name] = true
					}
				}
				warnings = append(warnings, expandRefs(cmd, set)...)
			}
		}
	}
	for _, key := range sortedKeys(overrides) {
		if !used[key] {
			warnings = append(warnings, fmt.Sprintf("params: no command named %q", key))
		}
	}
	return warnings
}

// overrideParam applies o to cmd's param name, reporting whether it set
// the default (so ${refs} in it get expanded).
func overrideParam(cmd *ResolvedCommand, name string, o ParamOverride) (bool, []string) {
	var p *Param
	for i := range cmd.Params {
		if cmd.Params[i].Name == name {
			p = &cmd.Params[i]
			break
		}
	}
	if p == nil {
		return false, []string{fmt.Sprintf("params: %s has no param %q", cmd.Name, name)}
	}
	if len(o.Options) == 0 && o.Default == nil {
		return false, []string{fmt.Sprintf("params: %s.%s: set options, default, or both", cmd.Name, name)}
	}

	var warnings []string
	if len(o.Options) > 0 {
		if p.Type != ParamSelect && p.Type != ParamMulti {
			warnings = append(warnings, fmt.Sprintf("params: %s.%s: options only apply to select and multi params, not %s", cmd.Name, name, p.Type))
		} else {
			p.Options = append([]string(nil), o.Options...)
			p.OptionsCmd = ""
			p.DependsOn = nil
			if p.Default != "" && notAnOption(*p, p.Default) != "" {
				p.Default = "" // the command's own default isn't among the new options
			}
		}
	}

	if o.Default == nil {
		return false, warnings
	}
	val, err := defaultString(o.Default, p.Type)
	if err != nil {
		return false, append(warnings, fmt.Sprintf("params: %s.%s: %v", cmd.Name, name, err))
	}
	if bad := notAnOption(*p, val); bad != "" {
		return false, append(warnings, fmt.Sprintf("params: %s.%s: %q is not one of %s", cmd.Name, name, bad, strings.Join(p.Options, ", ")))
	}
	p.Default = val
	return true, warnings
}

// notAnOption returns the first value in val (a select's value, or a
// multi's comma-separated values) that isn't among p's fixed options, or
// "" when all are. Params whose options come from options_cmd, or that
// have no options, accept anything, since their choices aren't known yet.
func notAnOption(p Param, val string) string {
	if p.OptionsCmd != "" || len(p.Options) == 0 || val == "" {
		return ""
	}
	switch p.Type {
	case ParamSelect:
		if !contains(p.Options, val) {
			return val
		}
	case ParamMulti:
		for _, v := range strings.Split(val, ",") {
			if v = strings.TrimSpace(v); v != "" && !contains(p.Options, v) {
				return v
			}
		}
	}
	return ""
}
