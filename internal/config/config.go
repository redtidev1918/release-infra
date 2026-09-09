package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

func Unmarshal(raw []byte, target any) error {
	var head struct {
		APIVersion string `yaml:"apiVersion"`
	}
	if err := yaml.Unmarshal(raw, &head); err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if head.APIVersion == "releasegraph.dev/v1" {
		decoder.KnownFields(true)
	}
	return decoder.Decode(target)
}
