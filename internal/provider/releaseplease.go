package provider

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/redtidev1918/releasegraph/internal/domain"
	"github.com/redtidev1918/releasegraph/internal/github"
	"github.com/redtidev1918/releasegraph/internal/policy"
	"github.com/redtidev1918/releasegraph/internal/registry"
)

// Context identifies one version release across the provider boundary.
type Context struct {
	Repository string         `json:"repository"`
	Version    domain.Version `json:"version"`
	Tag        string         `json:"tag"`
	ReleasePR  int            `json:"releasePr,omitempty"`
	PRMergeSHA string         `json:"prMergeSha,omitempty"`
	Provider   Kind           `json:"provider"`
	Labels     []string       `json:"labels,omitempty"`
}

// Report is the full reconciliation report for one version.
type Report struct {
	Context    Context         `json:"context"`
	Observed   Observed        `json:"observed"`
	Verdict    Verdict         `json:"verdict"`
	PlannedACK []LabelMutation `json:"plannedAck,omitempty"`
}

const (
	labelPending   = "autorelease: pending"
	labelTriggered = "autorelease: triggered"
	labelTagged    = "autorelease: tagged"
)

// releasePRPattern matches release-please PR titles like
// "chore(main): release 2.16.0" and component variants.
var releasePRPattern = regexp.MustCompile(`release[ :]+v?(\d+\.\d+[\w.\-+]*)`)

type releaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type releaseAPI struct {
	TagName    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	HTMLURL    string         `json:"html_url"`
	Assets     []releaseAsset `json:"assets"`
}

// ResolveProvider maps a policy versioning mode to a provider kind.
func ResolveProvider(p *policy.Policy) Kind {
	mode := p.Versioning.Mode
	if mode == "" {
		mode = p.Versioning.Provider
	}
	switch mode {
	case "release-please":
		return KindReleasePlease
	case "manual":
		return KindManual
	default:
		return KindTag
	}
}

// Inspect gathers actual release state and provider acknowledgement for one
// version of a repository and classifies the drift. No mutations.
func Inspect(ctx context.Context, client *github.Client, verifier *registry.Verifier, p *policy.Policy, repo, version string) (*Report, error) {
	tag := policy.ReleaseTag(p, version)
	provider := ResolveProvider(p)

	report := &Report{Context: Context{Repository: repo, Version: domain.Version(version), Tag: tag, Provider: provider}}

	// Provider side: find the merged release PR and its labels.
	if provider == KindReleasePlease {
		pr, state, err := providerState(ctx, client, repo, version)
		if err != nil {
			return nil, err
		}
		report.Observed.ProviderState = state
		if pr != nil {
			report.Context.ReleasePR = pr.Number
			report.Context.PRMergeSHA = pr.MergeCommitSHA
			for _, l := range pr.Labels {
				report.Context.Labels = append(report.Context.Labels, l.Name)
			}
		}
	} else {
		report.Observed.ProviderState = StateNone
	}

	actual := Actual{ExpectedCommit: report.Context.PRMergeSHA}

	// Tag (peel annotated tags to the commit).
	tagCommit, err := client.TagCommit(ctx, repo, tag)
	if err != nil {
		return nil, err
	}
	actual.TagExists = tagCommit != ""
	actual.TagCommit = tagCommit

	// GitHub Release.
	var rel releaseAPI
	found, err := client.GetOptional(ctx, fmt.Sprintf("repos/%s/releases/tags/%s", repo, tag), &rel)
	if err != nil {
		return nil, err
	}
	actual.ReleaseExists = found && !rel.Draft

	if found {
		actual.AssetsComplete, actual.ChecksumsVerified = assetState(p, rel.Assets, client, ctx, repo)

		// Latest is only meaningful for stable releases.
		if !rel.Prerelease {
			var latest releaseAPI
			latestFound, err := client.GetOptional(ctx, fmt.Sprintf("repos/%s/releases/latest", repo), &latest)
			if err != nil {
				return nil, err
			}
			actual.Latest = latestFound && latest.TagName == tag
		} else {
			actual.Latest = true
		}
	}

	// Required registries.
	actual.NoRegistryRequired, actual.RegistriesHealthy = registryState(ctx, client, verifier, p, repo, version)

	report.Observed.Provider = provider
	report.Observed.Version = domain.Version(version)
	report.Observed.Actual = actual
	report.Verdict = Classify(report.Observed)
	return report, nil
}

// providerState finds the merged release PR for version and maps its labels.
func providerState(ctx context.Context, client *github.Client, repo, version string) (*github.PullRequest, State, error) {
	prs, err := client.MergedPullRequests(ctx, repo)
	if err != nil {
		return nil, StateUnknown, err
	}
	var pr *github.PullRequest
	for i := range prs {
		candidate := &prs[i]
		if m := releasePRPattern.FindStringSubmatch(candidate.Title); m != nil && m[1] == version {
			pr = candidate
			break
		}
	}
	if pr == nil {
		// No merged release PR despite release-please mode is unreadable to us.
		return nil, StateUnknown, nil
	}
	labels := map[string]bool{}
	for _, l := range pr.Labels {
		labels[l.Name] = true
	}
	switch {
	case labels[labelTagged]:
		return pr, StateTagged, nil
	case labels[labelTriggered]:
		return pr, StateTriggered, nil
	case labels[labelPending]:
		return pr, StatePending, nil
	default:
		// Merged release PR with no autorelease label. release-please itself
		// lands in this shape after a successful run; treat as acknowledged
		// only when the title/version matched, else unknown.
		return pr, StateTagged, nil
	}
}

func assetState(p *policy.Policy, assets []releaseAsset, client *github.Client, ctx context.Context, repo string) (complete, sumsVerified bool) {
	byName := map[string]releaseAsset{}
	for _, a := range assets {
		byName[a.Name] = a
	}
	required := append([]string{}, p.Assets.Required...)
	required = append(required, "RELEASE-METADATA.json")
	checksumsEnabled := p.Checksums && len(p.Assets.Required) > 0
	if checksumsEnabled {
		required = append(required, "SHA256SUMS")
	}
	complete = true
	for _, pattern := range required {
		matched := false
		for _, a := range assets {
			if ok, _ := path.Match(pattern, a.Name); ok && a.Size > 0 {
				matched = true
				break
			}
		}
		if !matched {
			complete = false
		}
	}
	if !checksumsEnabled {
		return complete, complete
	}
	sumsAsset, ok := byName["SHA256SUMS"]
	if !ok {
		return complete, false
	}
	text, found, err := client.ReleaseAssetText(ctx, repo, sumsAsset.ID)
	if err != nil || !found {
		return complete, false
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			listed[strings.TrimPrefix(fields[1], "*")] = true
		}
	}
	covers := true
	for _, pattern := range p.Assets.Required {
		matched := false
		for name := range listed {
			if ok, _ := path.Match(pattern, name); ok {
				matched = true
				break
			}
		}
		if !matched {
			covers = false
		}
	}
	return complete, complete && covers
}

func registryState(ctx context.Context, client *github.Client, verifier *registry.Verifier, p *policy.Policy, repo, version string) (noneRequired, healthy bool) {
	healthy = true
	any := false
	names := make([]string, 0, len(p.Registries))
	for name := range p.Registries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cfg := p.Registries[name]
		if name == "github" || !cfg.Required {
			continue
		}
		any = true
		if err := verifier.Verify(ctx, name, cfg, repo, version, nil); err != nil {
			healthy = false
		}
	}
	return !any, healthy
}

// Acknowledge reconciles provider labels after a healthy release. It is
// idempotent: no tag/release mutation, label-only. dryRun returns the plan
// without applying it.
func Acknowledge(ctx context.Context, client *github.Client, report *Report, dryRun bool) ([]LabelMutation, error) {
	if !report.Verdict.ACKAllowed {
		return nil, fmt.Errorf("ACK refused: %s (%s)", report.Verdict.Health, report.Verdict.Drift)
	}
	if report.Context.ReleasePR == 0 {
		return nil, fmt.Errorf("ACK refused: no release PR resolved for %s %s", report.Context.Repository, report.Context.Version)
	}
	mutations := PlanACK(report.Context.Labels, nil, nil)
	if dryRun || len(mutations) == 0 {
		return mutations, nil
	}
	// Add first so an interrupted run can never leave the PR without an ACK
	// label; remove pending afterwards. Both calls are independently idempotent.
	var toAdd, toRemove []string
	for _, m := range mutations {
		switch m.Action {
		case "add":
			toAdd = append(toAdd, m.Label)
		case "remove":
			toRemove = append(toRemove, m.Label)
		}
	}
	if len(toAdd) > 0 {
		if err := client.AddIssueLabels(ctx, report.Context.Repository, report.Context.ReleasePR, toAdd); err != nil {
			return nil, err
		}
	}
	for _, label := range toRemove {
		if err := client.RemoveIssueLabel(ctx, report.Context.Repository, report.Context.ReleasePR, label); err != nil {
			return nil, err
		}
	}
	return mutations, nil
}
