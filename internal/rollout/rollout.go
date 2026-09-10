// Package rollout moves business repositories from one ReleaseGraph version to
// another using immutable refs.
//
// A mutable channel alias such as @v1 gives a new ReleaseGraph version to the
// whole fleet the moment it is pushed. Rollout replaces that with an explicit,
// reviewable pin: the canary repository is upgraded first, and the fleet only
// follows once the canary has completed a release lifecycle on the new version.
package rollout

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/redtidev1918/releasegraph/internal/fleet"
	"github.com/redtidev1918/releasegraph/internal/github"
)

// WorkflowPath is the release workflow businesses call and the file rollout edits.
const WorkflowPath = ".github/workflows/release.yml"

// MutableChannel matches channel aliases such as v1 or v2. These are convenient
// but give no blast-radius control, so they are reported, never silently kept.
var MutableChannel = regexp.MustCompile(`^v\d+$`)

// Pin is the ReleaseGraph version a repository currently calls.
type Pin struct {
	// Ref is the value after "@" in the uses: line. Empty when the workflow does
	// not call ReleaseGraph at all.
	Ref string `json:"ref"`
	// Uses is the full uses: reference for auditability.
	Uses string `json:"uses,omitempty"`
	// Mutable reports whether Ref is a channel alias instead of an exact version.
	Mutable bool `json:"mutable"`
	// Exact reports whether Ref looks like an immutable version tag.
	Exact bool `json:"exact"`
}

var usesPattern = regexp.MustCompile(`uses:\s*([^\s#]+)`)

// ParsePin extracts the ReleaseGraph pin from a release workflow definition.
func ParsePin(workflow []byte) Pin {
	for _, line := range strings.Split(string(workflow), "\n") {
		match := usesPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		uses := match[1]
		if !strings.Contains(uses, "releasegraph/") || !strings.Contains(uses, "reusable-release.yml") {
			continue
		}
		ref := ""
		if idx := strings.LastIndex(uses, "@"); idx >= 0 {
			ref = uses[idx+1:]
		}
		return Pin{Ref: ref, Uses: uses, Mutable: MutableChannel.MatchString(ref), Exact: semverTag.MatchString(ref)}
	}
	return Pin{}
}

var semverTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// Status of one repository in a rollout plan.
const (
	StatusCurrent     = "CURRENT"         // already on the target version
	StatusReady       = "READY"           // may be upgraded now
	StatusCanaryFirst = "CANARY_REQUIRED" // the canary must pass before this one moves
	StatusBlocked     = "BLOCKED"         // the canary has not passed
	StatusNoPin       = "NO_PIN"          // no ReleaseGraph call found
	StatusUnmanaged   = "UNMANAGED"       // not declared in fleet.yaml
)

// Entry is one repository's rollout state.
type Entry struct {
	Repository     string `json:"repository"`
	Classification string `json:"classification"`
	Canary         bool   `json:"canary,omitempty"`
	Current        Pin    `json:"current"`
	Target         string `json:"target"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
}

// Plan is the full rollout decision for one target version.
type Plan struct {
	Target       string   `json:"target"`
	Canary       string   `json:"canary,omitempty"`
	CanaryPassed bool     `json:"canaryPassed"`
	CanaryReason string   `json:"canaryReason,omitempty"`
	Entries      []Entry  `json:"entries"`
	Ready        []string `json:"ready"`
	Blocked      []string `json:"blocked"`
}

// CanaryEvidence is what a canary repository proves before a fleet rollout.
type CanaryEvidence struct {
	Pinned    bool   `json:"pinned"`
	Lifecycle bool   `json:"lifecyclePassed"`
	Reason    string `json:"reason,omitempty"`
}

// BuildPlan decides, for every managed repository, whether it may move to target.
//
// Invariant: the fleet stays blocked until the canary repository runs the target
// version and its latest release lifecycle succeeded. Rollout never assumes that
// a published version is a verified version.
func BuildPlan(manifest *fleet.Manifest, entries []Entry, target, canary string, evidence CanaryEvidence) Plan {
	plan := Plan{Target: target, Canary: canary, CanaryPassed: evidence.Pinned && evidence.Lifecycle, CanaryReason: evidence.Reason, Entries: []Entry{}, Ready: []string{}, Blocked: []string{}}

	for i := range entries {
		entry := &entries[i]
		switch entry.Classification {
		case fleet.ClassificationManaged:
		case fleet.ClassificationArchived, fleet.ClassificationDisabled:
			entry.Status = StatusUnmanaged
			entry.Reason = "not operable (" + entry.Classification + ")"
			plan.Entries = append(plan.Entries, *entry)
			continue
		default:
			entry.Status = StatusUnmanaged
			entry.Reason = "not declared in fleet.yaml"
			plan.Entries = append(plan.Entries, *entry)
			continue
		}

		switch {
		case entry.Current.Ref == "":
			entry.Status = StatusNoPin
			entry.Reason = "no ReleaseGraph reusable workflow call found"
		case entry.Current.Ref == entry.Target:
			entry.Status = StatusCurrent
			entry.Reason = "already on " + entry.Target
		case entry.Canary:
			entry.Status = StatusReady
			entry.Reason = "canary upgrade is always allowed first"
		case !plan.CanaryPassed:
			entry.Status = StatusBlocked
			entry.Reason = "canary " + canary + " has not passed on " + entry.Target
			plan.Blocked = append(plan.Blocked, entry.Repository)
		default:
			entry.Status = StatusReady
			entry.Reason = "canary passed on " + entry.Target
		}
		if entry.Status == StatusReady {
			plan.Ready = append(plan.Ready, entry.Repository)
		}
		plan.Entries = append(plan.Entries, *entry)
	}
	return plan
}

// InspectPins reads every managed repository's release workflow pin.
func InspectPins(ctx context.Context, client *github.Bound, manifest *fleet.Manifest, managed []fleet.Resolved, target string) []Entry {
	entries := make([]Entry, 0, len(managed))
	for _, repo := range managed {
		entry := Entry{Repository: repo.Name, Classification: repo.Classification, Canary: repo.Canary, Target: target}
		raw, found, err := client.ReadFile(ctx, repo.Name, WorkflowPath, "")
		switch {
		case err != nil:
			entry.Current = Pin{}
			entry.Reason = err.Error()
		case !found:
			entry.Current = Pin{}
			entry.Reason = WorkflowPath + " is missing"
		default:
			entry.Current = ParsePin(raw)
		}
		entries = append(entries, entry)
	}
	return entries
}

// VersionOfRef normalises a tag or channel ref to a version string for display.
func VersionOfRef(ref string) string { return strings.TrimPrefix(ref, "v") }

var usesRefPattern = regexp.MustCompile(`(releasegraph/[^\s@#]*reusable-release\.yml@)([^\s#]+)`)

// Repin rewrites the ReleaseGraph ref inside a workflow file. Anything that is
// not the ReleaseGraph reusable workflow call is left untouched, so comments and
// unrelated actions survive.
func Repin(workflow, version string) (string, error) {
	if !usesRefPattern.MatchString(workflow) {
		return "", fmt.Errorf("no ReleaseGraph reusable workflow reference found")
	}
	return usesRefPattern.ReplaceAllString(workflow, "${1}"+version), nil
}
