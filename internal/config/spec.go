package config

// ParamType is the kind of value a param collects, which maps to a huh
// field when the params form is built.
type ParamType string

const (
	ParamString ParamType = "string"
	ParamSelect ParamType = "select"
	ParamMulti  ParamType = "multi"
	ParamBool   ParamType = "bool"
	ParamSecret ParamType = "secret"
	ParamText   ParamType = "text"
)

type Param struct {
	Name       string     `yaml:"name"`
	Type       ParamType  `yaml:"type"`
	Default    string     `yaml:"default,omitempty"`
	Pattern    string     `yaml:"pattern,omitempty"`
	Required   bool       `yaml:"required,omitempty"`
	Options    OptionList `yaml:"options,omitempty"`
	OptionsCmd string     `yaml:"options_cmd,omitempty"`
	DependsOn  []string   `yaml:"depends_on,omitempty"`
	// Label derives labels from option values that are paths:
	// "basename" or "relative" (to the repo root). See options.go.
	Label string `yaml:"label,omitempty"`
}

// Command is the one spec shape shared by commands.yaml entries and
// sidecar files. A sidecar decodes directly into this struct; a
// commands.yaml entry decodes into the value side of CommandsFile.Commands.
type Command struct {
	Desc   string  `yaml:"desc,omitempty"`
	Params []Param `yaml:"params,omitempty"`
	Run    string  `yaml:"run,omitempty"`
	Script string  `yaml:"script,omitempty"`
	// Args are passed to the script, one argument per entry, with {param}
	// replaced by the param's value. Needs script (or an adjacent script
	// for a sidecar); see args.go.
	Args []string `yaml:"args,omitempty"`
}

// CommandsFile is the root shape of a project's commands.yaml.
type CommandsFile struct {
	Commands map[string]Command `yaml:"commands"`
}
