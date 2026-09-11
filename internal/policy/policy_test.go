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

func TestRepositoryPolicyLoads(t *testing.T) {
	p, err := Load("../../.release-policy.yml")
	if err != nil {
		t.Fatal(err)
	}
	if p.APIVersion != "releasegraph.dev/v1" || p.Retention.Stable != 1 || !p.Metadata {
		t.Fatalf("policy=%+v", p)
	}
}

func validProductionOperations() *ProductionOperations {
	return &ProductionOperations{
		Base:     "default",
		Branches: []string{"chore/cutover-*", "ops/*", "release/*", "hotfix/*"},
		Operations: map[string]OperationScope{
			"cutover": {Branches: []string{"chore/cutover-*"}, AllowedPaths: []string{"fly/*.toml", ".github/workflows/**"}},
		},
	}
}

func TestProductionOperationsValidation(t *testing.T) {
	base := func() *Policy {
		return &Policy{Kind: "none", Versioning: Versioning{Mode: "manual", Version: "1.0.0"}, Repository: Repository{Git: GitPolicy{ProductionOperations: validProductionOperations()}}}
	}
	if err := Validate(base()); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Policy)
	}{
		{"empty base", func(p *Policy) { p.Repository.Git.ProductionOperations.Base = "" }},
		{"no branch patterns", func(p *Policy) { p.Repository.Git.ProductionOperations.Branches = nil }},
		{"requireLatestBase false", func(p *Policy) { v := false; p.Repository.Git.ProductionOperations.RequireLatestBase = &v }},
		{"invalid glob", func(p *Policy) { p.Repository.Git.ProductionOperations.Branches = []string{"chore/cutover-["} }},
		{"operation without branches", func(p *Policy) { p.Repository.Git.ProductionOperations.Operations["x"] = OperationScope{} }},
		{"operation invalid allowedPath", func(p *Policy) {
			p.Repository.Git.ProductionOperations.Operations["cutover"] = OperationScope{Branches: []string{"chore/cutover-*"}, AllowedPaths: []string{"a["}}
		}},
	}
	for _, tc := range cases {
		p := base()
		tc.mutate(p)
		if err := Validate(p); err == nil {
			t.Errorf("%s: expected policy error", tc.name)
		}
	}
}

func TestRequireLatestDefaultsTrue(t *testing.T) {
	po := validProductionOperations()
	if !po.RequireLatest() {
		t.Fatal("nil RequireLatestBase must default to enforced")
	}
	v := true
	po.RequireLatestBase = &v
	if !po.RequireLatest() {
		t.Fatal("explicit true must stay enforced")
	}
}
