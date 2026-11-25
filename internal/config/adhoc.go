package config

// AdhocID is the stable id of the built-in "run any command" entry. The
// UI keys off it to name the resulting job after what was typed.
const AdhocID = "adhoc:run"

// AdhocGroup is the built-in group shown above Global: one entry whose
// form asks for a command line and runs it as a normal job (so it gets
// the jobs panel, logs, input prompts and background handling like any
// other). The typed text reaches the shell as $LC_COMMAND and is run
// with eval in the repo root; nothing is parsed or rewritten.
func AdhocGroup() Group {
	return Group{
		Title: "Ad-hoc",
		Commands: []ResolvedCommand{{
			ID:   AdhocID,
			Name: "run command…",
			Desc: "Type any shell command and run it as a job in this directory.",
			Mode: ModePanel,
			Params: []Param{
				{Name: "command", Type: ParamString, Required: true},
			},
			Run: `eval "$LC_COMMAND"`,
			Preview: func(v map[string]string) string {
				return v["command"]
			},
		}},
	}
}
