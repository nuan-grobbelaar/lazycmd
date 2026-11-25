package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Option is one choice of a select or multi param. Value is what the
// command receives (LC_<NAME>, args:, the preview) and what defaults
// match against; Label is only what the form shows. An empty Label means
// "show the value", or a label derived from it by the param's label mode.
type Option struct {
	Label string
	Value string
}

// Display is the text the form shows for o.
func (o Option) Display() string {
	if o.Label != "" {
		return o.Label
	}
	return o.Value
}

// OptionList is a param's options. In YAML each entry is either a plain
// string (the value, shown as is) or a map with value and an optional
// label:
//
//	options:
//	  - dev
//	  - label: vandag
//	    value: /Users/me/repos/vandag
type OptionList []Option

// Opts builds an OptionList of plain values, labels left to the value.
func Opts(values ...string) OptionList {
	out := make(OptionList, len(values))
	for i, v := range values {
		out[i] = Option{Value: v}
	}
	return out
}

// Values returns just the values, in order.
func (l OptionList) Values() []string {
	out := make([]string, len(l))
	for i, o := range l {
		out[i] = o.Value
	}
	return out
}

// Has reports whether v is one of the values.
func (l OptionList) Has(v string) bool {
	for _, o := range l {
		if o.Value == v {
			return true
		}
	}
	return false
}

// UnmarshalYAML decodes the list by hand, so a map entry with an unknown
// key is an error like everywhere else in lazycmd's files (a custom
// decoder doesn't inherit the strict decoding of the file around it).
func (l *OptionList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: options must be a list", n.Line)
	}
	out := make(OptionList, 0, len(n.Content))
	for _, item := range n.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			out = append(out, Option{Value: scalarValue(item)})
		case yaml.MappingNode:
			var o Option
			hasValue := false
			for i := 0; i+1 < len(item.Content); i += 2 {
				k, v := item.Content[i], item.Content[i+1]
				if v.Kind != yaml.ScalarNode {
					return fmt.Errorf("line %d: option %s must be a plain value", v.Line, k.Value)
				}
				switch k.Value {
				case "value":
					o.Value, hasValue = scalarValue(v), true
				case "label":
					o.Label = scalarValue(v)
				default:
					return fmt.Errorf("line %d: option has unknown key %q (want value and label)", k.Line, k.Value)
				}
			}
			if !hasValue {
				return fmt.Errorf("line %d: option needs a value", item.Line)
			}
			out = append(out, o)
		default:
			return fmt.Errorf("line %d: an option is a string, or a map with value and label", item.Line)
		}
	}
	*l = out
	return nil
}

func scalarValue(n *yaml.Node) string {
	if n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// Label modes derive a label from each option's value, for options that
// are paths. They never change values, and an option with its own label
// keeps it.
const (
	LabelBasename = "basename" // last path element: /a/b/pom.xml -> pom.xml
	LabelRelative = "relative" // relative to the repo root: /repo/api/pom.xml -> api/pom.xml
)

func validLabelMode(m string) bool {
	return m == "" || m == LabelBasename || m == LabelRelative
}

// ParseOptionLines turns options_cmd output into options: one per
// non-empty line, "value<TAB>label" when the line has a tab, else the
// line is the value.
func ParseOptionLines(out string) OptionList {
	var opts OptionList
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		value, label, _ := strings.Cut(line, "\t")
		value, label = strings.TrimSpace(value), strings.TrimSpace(label)
		if value == "" {
			continue
		}
		opts = append(opts, Option{Label: label, Value: value})
	}
	return opts
}

// WithLabels returns opts with labels derived by mode for every option
// that has none. root is the repo root, for LabelRelative. When basename
// labels collide (two pom.xml files), the colliding ones get their parent
// folder too, and if that still collides, the full value.
func WithLabels(opts OptionList, mode, root string) OptionList {
	if mode == "" || len(opts) == 0 {
		return opts
	}
	out := append(OptionList(nil), opts...)
	for i, o := range opts {
		if o.Label != "" {
			continue
		}
		if mode == LabelRelative {
			out[i].Label = relativeLabel(o.Value, root)
		} else {
			out[i].Label = lastElems(o.Value, 1)
		}
	}
	if mode != LabelBasename {
		return out
	}
	// Two passes: colliding derived labels get their parent folder, then
	// any that still collide get the full value.
	for _, longer := range []func(string) string{
		func(v string) string { return lastElems(v, 2) },
		func(v string) string { return v },
	} {
		count := map[string]int{}
		for _, o := range out {
			count[o.Label]++
		}
		for i, o := range opts {
			if o.Label == "" && count[out[i].Label] > 1 {
				out[i].Label = longer(o.Value)
			}
		}
	}
	return out
}

// lastElems is the last n elements of a slash path, without a trailing
// slash ("/a/b/c", 2 -> "b/c").
func lastElems(v string, n int) string {
	p := strings.TrimRight(filepath.ToSlash(v), "/")
	if p == "" {
		return v
	}
	parts := strings.Split(p, "/")
	if len(parts) > n {
		parts = parts[len(parts)-n:]
	}
	if s := strings.Join(parts, "/"); s != "" {
		return s
	}
	return v
}

// relativeLabel shows v relative to root: an absolute path inside root
// loses the root, a relative one loses its "./". Anything outside root
// is shown as is.
func relativeLabel(v, root string) string {
	if filepath.IsAbs(v) {
		if root == "" {
			return v
		}
		rel, err := filepath.Rel(root, v)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return v
		}
		return filepath.ToSlash(rel)
	}
	if c := filepath.ToSlash(filepath.Clean(v)); c != ".." && !strings.HasPrefix(c, "../") {
		return c
	}
	return v
}
