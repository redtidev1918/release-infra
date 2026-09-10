package rollout

import (
	"strings"
	"testing"

	"github.com/redtidev1918/releasegraph/internal/fleet"
)

const workflowYAML = `name: Release
jobs:
  release:
    uses: redtidev1918/releasegraph/.github/workflows/reusable-release.yml@v1
    secrets: inherit
`

func testManifest(t *testing.T) *fleet.Manifest {
	t.Helper()
	manifest := &fleet.Manifest{Version: 1, Repositories: []fleet.ManifestEntry{
		{Name: "acme/app", Canary: true},
		{Name: "acme/lib"},
		{Name: "acme/deploy"},
	}}
	return manifest
}

func entry(name, ref string, canary bool, classification string) Entry {
	return Entry{Repository: name, Classification: classification, Canary: canary, Target: "v1.4.1", Current: ParsePin([]byte(replaceRef(workflowYAML, ref)))}
}

func replaceRef(workflow, ref string) string {
	pin := ParsePin([]byte(workflow))
	if pin.Ref == "" {
		return workflow
	}
	return strings.ReplaceAll(workflow, "@"+pin.Ref, "@"+ref)
}

// Acceptance: a mutable channel alias is visible, and an exact version is not.
func TestParsePinDistinguishesMutableChannel(t *testing.T) {
	mutable := ParsePin([]byte(workflowYAML))
	if mutable.Ref != "v1" || !mutable.Mutable || mutable.Exact {
		t.Fatalf("mutable pin = %+v", mutable)
	}
	exact := ParsePin([]byte(replaceRef(workflowYAML, "v1.4.1")))
	if exact.Ref != "v1.4.1" || exact.Mutable || !exact.Exact {
		t.Fatalf("exact pin = %+v", exact)
	}
	if empty := ParsePin([]byte("name: ci\n")); empty.Ref != "" {
		t.Fatalf("unrelated workflow produced a pin: %+v", empty)
	}
}

// Acceptance: a new version goes to the canary first; the fleet stays blocked
// until the canary has actually passed on that version.
func TestCanaryGatesFleetRollout(t *testing.T) {
	entries := []Entry{
		entry("acme/app", "v1.4.0", true, fleet.ClassificationManaged),
		entry("acme/lib", "v1.4.0", false, fleet.ClassificationManaged),
		entry("acme/deploy", "v1.4.0", false, fleet.ClassificationManaged),
	}

	blocked := BuildPlan(testManifest(t), entries, "v1.4.1", "acme/app", CanaryEvidence{Reason: "canary is pinned to v1.4.0"})
	if len(blocked.Blocked) != 2 {
		t.Fatalf("blocked = %v, want the two non-canary repositories", blocked.Blocked)
	}
	if len(blocked.Ready) != 1 || blocked.Ready[0] != "acme/app" {
		t.Fatalf("ready = %v, want only the canary", blocked.Ready)
	}

	passed := BuildPlan(testManifest(t), entries, "v1.4.1", "acme/app", CanaryEvidence{Pinned: true, Lifecycle: true})
	if len(passed.Ready) != 3 || len(passed.Blocked) != 0 {
		t.Fatalf("after canary pass: ready=%v blocked=%v", passed.Ready, passed.Blocked)
	}
}

// A canary that is pinned but whose lifecycle never succeeded does not unblock.
func TestCanaryPinnedWithoutSuccessfulLifecycleStaysBlocked(t *testing.T) {
	entries := []Entry{entry("acme/lib", "v1.4.0", false, fleet.ClassificationManaged)}
	plan := BuildPlan(testManifest(t), entries, "v1.4.1", "acme/app", CanaryEvidence{Pinned: true, Reason: "canary run concluded failure"})
	if len(plan.Blocked) != 1 {
		t.Fatalf("blocked = %v, want the repository to stay blocked", plan.Blocked)
	}
}

// Acceptance: a repository already on the target is a NOOP, not a repeated change.
func TestAlreadyOnTargetIsCurrent(t *testing.T) {
	entries := []Entry{entry("acme/lib", "v1.4.1", false, fleet.ClassificationManaged)}
	plan := BuildPlan(testManifest(t), entries, "v1.4.1", "acme/app", CanaryEvidence{Pinned: true, Lifecycle: true})
	if plan.Entries[0].Status != StatusCurrent {
		t.Fatalf("status = %s, want CURRENT", plan.Entries[0].Status)
	}
	if len(plan.Ready) != 0 {
		t.Fatalf("ready = %v, want nothing to do", plan.Ready)
	}
}

// Acceptance: a repository outside fleet.yaml is never rolled out.
func TestUndeclaredRepositoryIsNotRolledOut(t *testing.T) {
	entries := []Entry{{Repository: "acme/mystery", Classification: fleet.ClassificationDiscoveredUnmanaged, Target: "v1.4.1", Current: ParsePin([]byte(workflowYAML))}}
	plan := BuildPlan(testManifest(t), entries, "v1.4.1", "acme/app", CanaryEvidence{Pinned: true, Lifecycle: true})
	if plan.Entries[0].Status != StatusUnmanaged || len(plan.Ready) != 0 {
		t.Fatalf("undeclared repository was rolled out: %+v", plan.Entries[0])
	}
}

func TestRepinOnlyRewritesTheReleaseGraphReference(t *testing.T) {
	source := `name: Release
jobs:
  a:
    uses: actions/checkout@v7
  b:
    uses: redtidev1918/releasegraph/.github/workflows/reusable-release.yml@v1
  c:
    uses: redtidev1918/releasegraph/.github/workflows/reusable-release.yml@v1 # keep comment
`
	next, err := Repin(source, "v1.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, "reusable-release.yml@v1.4.1") {
		t.Fatalf("ref not updated:\n%s", next)
	}
	if !strings.Contains(next, "actions/checkout@v7") {
		t.Fatalf("unrelated action was rewritten:\n%s", next)
	}
	if !strings.Contains(next, "# keep comment") {
		t.Fatalf("comment lost:\n%s", next)
	}
	if _, err := Repin("name: nothing\n", "v1.4.1"); err == nil {
		t.Fatal("a workflow without a ReleaseGraph call must be an error")
	}
}
