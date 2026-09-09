package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDoctorJSONEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var envelope Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.ReleaseGraphVersion == "" || envelope.GeneratedAt == "" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestGraphMermaid(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"graph", "--file", "../../examples/control/release-graph.yml", "--format", "mermaid"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, edge := range []string{"core --> cli", "core --> web", "cli --> deploy", "web --> deploy"} {
		if !strings.Contains(text, edge) {
			t.Fatalf("missing %s:\n%s", edge, text)
		}
	}
}

func TestLivePlanRejectsFixtureState(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--graph", "../../testdata/graph/diamond.yml", "--live", "--state", "../../testdata/graph/diamond-health.json"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}
