package config

import "testing"

func TestVersionedConfigRejectsUnknownFields(t *testing.T) {
	var target struct {
		Name string `yaml:"name"`
	}
	if err := Unmarshal([]byte("apiVersion: releasegraph.dev/v1\nname: ok\nunexpected: true\n"), &target); err == nil {
		t.Fatal("expected unknown field error")
	}
}
