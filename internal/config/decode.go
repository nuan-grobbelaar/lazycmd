package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// decodeStrict decodes the YAML file at path into out, erroring on unknown
// fields so a typo in a command spec is a loud failure, not a silently
// ignored field.
func decodeStrict(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	return dec.Decode(out)
}
