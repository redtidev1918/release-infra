package policy

import "testing"

func TestLoadJSONPolicyAndComponentDesiredVersion(t *testing.T) {
	p, err := Load("../../testdata/policy/binary.json")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "binary" || !p.Checksums || p.Hash == "" {
		t.Fatalf("policy=%+v", p)
	}
	got, err := DesiredVersion(p, "", "../../testdata/policy")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.4.1" {
		t.Fatalf("desired=%q", got)
	}
}

func TestExplicitVersionNormalization(t *testing.T) {
	p := &Policy{Kind: "binary", Versioning: Versioning{Mode: "manual", Version: "1.2.3"}, Checksums: true}
	got, err := DesiredVersion(p, "v1.2.3", ".")
	if err != nil || got != "1.2.3" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestUnknownRegistryRejected(t *testing.T) {
	p := &Policy{Kind: "none", Versioning: Versioning{Mode: "manual", Version: "1.0.0"}, Registries: map[string]Registry{"bad": {}}, Checksums: true}
	if err := Validate(p); err == nil {
		t.Fatal("expected policy error")
	}
}

func TestReleaseTagPreservesLegacyTemplate(t *testing.T) {
	p := &Policy{Tag: Tag{Template: "cli-v{version}"}}
	if got := ReleaseTag(p, "1.2.3"); got != "cli-v1.2.3" {
		t.Fatalf("tag=%q", got)
	}
}
