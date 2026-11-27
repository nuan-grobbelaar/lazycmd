package config

import (
	"gopkg.in/yaml.v3"

	"lazycmd/internal/safefile"
)

// decodeStrict decodes the YAML file at path into out, erroring on unknown
// fields so a typo in a command spec is a loud failure, not a silently
// ignored field.
func decodeStrict(path string, out any) error {
	f, err := safefile.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := yaml.NewDecoder(safefile.Reader(f))
	dec.KnownFields(true)
	return dec.Decode(out)
}
