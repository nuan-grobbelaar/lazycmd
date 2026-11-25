package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Command arguments. A script run with `args:` gets an argument list
// built here instead of (or as well as) LC_ env vars, which lets a
// sidecar drive tools that were never written for lazycmd.
//
// Each args entry becomes exactly one argument, so a value with spaces,
// quotes or ; is still a single argument and nothing is ever parsed by a
// shell. In an entry, {name} is replaced by that param's value ({{ and
// }} are literal braces). An entry that uses placeholders and has every
// one of them empty is dropped, which is how optional flags work:
// "--env={env}" disappears when env is empty. A bool is "true" or
// "false" and a multi param is its choices joined with commas.

// argPart is one piece of a parsed args entry: literal text, or the
// name of a param when param is true.
type argPart struct {
	text  string
	param bool
}

func parseArg(s string) ([]argPart, error) {
	var parts []argPart
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, argPart{text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '{' && i+1 < len(s) && s[i+1] == '{':
			lit.WriteByte('{')
			i++
		case c == '}' && i+1 < len(s) && s[i+1] == '}':
			lit.WriteByte('}')
			i++
		case c == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unclosed { in %q (write {{ for a literal brace)", s)
			}
			name := s[i+1 : i+end]
			if name == "" || strings.ContainsAny(name, " {") {
				return nil, fmt.Errorf("bad placeholder {%s} in %q", name, s)
			}
			flush()
			parts = append(parts, argPart{text: name, param: true})
			i += end
		case c == '}':
			return nil, fmt.Errorf("stray } in %q (write }} for a literal brace)", s)
		default:
			lit.WriteByte(c)
		}
	}
	flush()
	return parts, nil
}

// validateCommand runs the load-time checks every command spec gets,
// whatever file it came from.
func validateCommand(cmd ResolvedCommand) error {
	if err := validateConfirm(cmd); err != nil {
		return err
	}
	if err := validateParams(cmd); err != nil {
		return err
	}
	return validateArgs(cmd)
}

// validateParams checks each param's pattern compiles, so a bad one is
// a load warning instead of a crash when the form opens.
// paramName is what a param may be called: it becomes LC_<NAME>, so it
// must be a name a shell can read back ($LC_MY_NAME). A space or "-"
// would make it unreadable, and "=" would split the variable
// ("a=b" sets LC_A to "B=value").
var paramName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateParams(cmd ResolvedCommand) error {
	seen := map[string]bool{}
	for _, p := range cmd.Params {
		if !paramName.MatchString(p.Name) {
			return fmt.Errorf("param %q: names may only use letters, digits and _, and can't start with a digit (it becomes $LC_%s)", p.Name, strings.ToUpper(p.Name))
		}
		key := strings.ToUpper(p.Name)
		if seen[key] {
			return fmt.Errorf("param %q is declared twice (names are case-insensitive: both become $LC_%s)", p.Name, key)
		}
		seen[key] = true
		if !validLabelMode(p.Label) {
			return fmt.Errorf("param %q: label must be %q or %q, not %q", p.Name, LabelBasename, LabelRelative, p.Label)
		}
		if p.Label != "" && p.Type != ParamSelect && p.Type != ParamMulti {
			return fmt.Errorf("param %q: label only applies to select and multi params", p.Name)
		}
		if p.Pattern == "" {
			continue
		}
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return fmt.Errorf("param %q: bad pattern: %v", p.Name, err)
		}
	}
	return nil
}

// validateArgs checks a command's args at load time: they need a script
// to run, and may only mention params the command declares. A secret
// param may not be used at all: arguments are visible to every user on
// the machine (ps), while the LC_ env var is readable only by you.
func validateArgs(cmd ResolvedCommand) error {
	if len(cmd.Args) == 0 {
		return nil
	}
	if cmd.ScriptPath == "" {
		return fmt.Errorf("args: needs a script to run; with run: use \"$LC_<NAME>\" in the shell body instead")
	}
	declared := map[string]ParamType{}
	for _, p := range cmd.Params {
		declared[p.Name] = p.Type
	}
	for _, a := range cmd.Args {
		parts, err := parseArg(a)
		if err != nil {
			return fmt.Errorf("args: %v", err)
		}
		for _, p := range parts {
			if !p.param {
				continue
			}
			typ, ok := declared[p.text]
			if !ok {
				return fmt.Errorf("args: {%s} in %q is not a declared param", p.text, a)
			}
			if typ == ParamSecret {
				return fmt.Errorf("args: {%s} is a secret, and secrets can't be passed as arguments (any user on the machine can read them with ps); read $LC_%s in the script instead", p.text, strings.ToUpper(p.text))
			}
		}
	}
	return nil
}

// ExpandArgs builds the argument list for a run from the args templates
// and the answered values. Templates were checked by validateArgs when
// loaded, so a malformed one here (which can't happen for a loaded
// command) is passed through literally rather than failing a run.
func ExpandArgs(args []string, values map[string]string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		parts, err := parseArg(a)
		if err != nil {
			out = append(out, a)
			continue
		}
		var b strings.Builder
		sawParam, allEmpty := false, true
		for _, p := range parts {
			if !p.param {
				b.WriteString(p.text)
				continue
			}
			sawParam = true
			v := values[p.text]
			if v != "" {
				allEmpty = false
			}
			b.WriteString(v)
		}
		if sawParam && allEmpty {
			continue
		}
		out = append(out, b.String())
	}
	return out
}
