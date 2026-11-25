package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfirmSpec is a command's confirm: key. true asks "Run <name>?"; a
// string is the question itself, with {param} placeholders filled in
// from the answers; false (or no key) asks nothing.
//
//	confirm: true
//	confirm: "Deploy {services} to {env}?"
type ConfirmSpec struct {
	Set  bool
	Text string // empty with Set means the default question
}

func (c *ConfirmSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		switch n.Tag {
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return err
			}
			*c = ConfirmSpec{Set: b}
			return nil
		case "!!str":
			if strings.TrimSpace(n.Value) == "" {
				return fmt.Errorf("line %d: confirm text is empty (use confirm: true for the default question)", n.Line)
			}
			*c = ConfirmSpec{Set: true, Text: n.Value}
			return nil
		}
	}
	return fmt.Errorf("line %d: confirm must be true, false or the question to ask", n.Line)
}

// confirmText is the question for spec on the command called name.
func confirmText(spec ConfirmSpec, name string) string {
	switch {
	case !spec.Set:
		return ""
	case spec.Text != "":
		return spec.Text
	}
	// The name is literal text, so its braces are escaped.
	return "Run " + strings.NewReplacer("{", "{{", "}", "}}").Replace(name) + "?"
}

// validateConfirm checks a confirm question's placeholders: well formed,
// and naming params the command declares.
func validateConfirm(cmd ResolvedCommand) error {
	if cmd.Confirm == "" {
		return nil
	}
	return checkConfirmText(cmd, cmd.Confirm)
}

func checkConfirmText(cmd ResolvedCommand, text string) error {
	parts, err := parseArg(text)
	if err != nil {
		return fmt.Errorf("confirm: %v", err)
	}
	declared := map[string]bool{}
	for _, p := range cmd.Params {
		declared[p.Name] = true
	}
	for _, p := range parts {
		if p.param && !declared[p.text] {
			return fmt.Errorf("confirm: {%s} is not a param of this command", p.text)
		}
	}
	return nil
}

// ExpandConfirm fills in a confirm question from the answers. Secrets
// show as dots and an empty answer as "(none)", so the question never
// reads as if a word went missing. The result is plain text for display;
// the caller cleans it.
func ExpandConfirm(cmd ResolvedCommand, values map[string]string) string {
	parts, err := parseArg(cmd.Confirm)
	if err != nil {
		return cmd.Confirm
	}
	secret := map[string]bool{}
	for _, p := range cmd.Params {
		if p.Type == ParamSecret {
			secret[p.Name] = true
		}
	}
	var b strings.Builder
	for _, p := range parts {
		switch {
		case !p.param:
			b.WriteString(p.text)
		case values[p.text] == "":
			b.WriteString("(none)")
		case secret[p.text]:
			b.WriteString("••••••")
		default:
			b.WriteString(values[p.text])
		}
	}
	return b.String()
}

// ApplyConfirm adds the confirms a profile's confirm: map asks for, keyed
// by command name or id as in defaults:. A profile can add a confirm or
// change its question, never remove one: false is a warning.
func ApplyConfirm(groups []Group, confirms map[string]any) []string {
	if len(confirms) == 0 {
		return nil
	}
	var warnings []string
	used := map[string]bool{}
	for gi := range groups {
		for ci := range groups[gi].Commands {
			cmd := &groups[gi].Commands[ci]
			for key, raw := range confirms {
				if key != cmd.Name && key != cmd.ID {
					continue
				}
				used[key] = true
				switch v := raw.(type) {
				case bool:
					if !v {
						warnings = append(warnings, fmt.Sprintf("confirm: %s: false does nothing; a profile can add a confirm but not remove one", key))
					} else if cmd.Confirm == "" {
						cmd.Confirm = confirmText(ConfirmSpec{Set: true}, cmd.Name)
					}
				case string:
					if err := checkConfirmText(*cmd, v); err != nil {
						warnings = append(warnings, fmt.Sprintf("%s: %v", key, err))
					} else if strings.TrimSpace(v) != "" {
						cmd.Confirm = v
					}
				default:
					warnings = append(warnings, fmt.Sprintf("confirm: %s: must be true or the question to ask", key))
				}
			}
		}
	}
	for _, key := range sortedKeys(confirms) {
		if !used[key] {
			warnings = append(warnings, fmt.Sprintf("confirm: no command named %q", key))
		}
	}
	return warnings
}
