package provider

import (
	"context"
	"encoding/json"
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
	// PolicyHashMatches reports whether the release was published under the
	// current policy; false means asset differences are contract drift, not an
	// incomplete release.
	PolicyHashMatches bool `json:"policyHashMatches"`
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
	// labelWaived is the explicit human waiver for an unrecoverable historical
	// version. It is deliberately a different namespace from release-please's
	// own labels so the two never overwrite each other.
	labelWaived = "releasegraph: historical-waived"
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
func Inspect(ctx context.Context, client *github.Bound, verifier *registry.Verifier, p *policy.Policy, repo, version string) (*Report, error) {
	tag := policy.ReleaseTag(p, version)
	provider := ResolveProvider(p)

	report := &Report{Context: Context{Repository: repo, Version: domain.Version(version), Tag: tag, Provider: provider}}
	capabilities := p.CapabilitiesOf()
	report.Observed.Capabilities = capabilities

	// Provider side: find the merged release PR and its labels.
	waived := false
	if provider == KindReleasePlease {
		pr, state, err := providerState(ctx, client, repo, version)
		if err != nil {
			return nil, err
		}
		report.Observed.ProviderState = state
		if pr != nil {
			for _, l := range pr.Labels {
				if l.Name == labelWaived {
					waived = true
				}
			}
			report.Context.ReleasePR = pr.Number
			report.Context.PRMergeSHA = pr.MergeCommitSHA
			for _, l := range pr.Labels {
				report.Context.Labels = append(report.Context.Labels, l.Name)
			}
		}
	} else {
		report.Observed.ProviderState = StateNone
	}

	actual := Actual{ExpectedCommit: report.Context.PRMergeSHA, Waived: waived}

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
		meta := readReleaseMetadata(ctx, client, repo, rel.Assets)
		// Without a release PR (manual / tag providers) the release's own
		// metadata records the commit it was built from; that is the expected
		// tag target. Judging such a provider against an empty commit would
		// report every healthy manual release as incomplete forever.
		if actual.ExpectedCommit == "" {
			actual.ExpectedCommit = meta.CommitSHA
		}
		// A historical release keeps the contract it was published under.
		if meta.Capabilities != nil {
			caps := policy.Capabilities{
				GitHubRelease: meta.Capabilities.GitHubRelease,
				Binaries:      meta.Capabilities.Binaries,
				Checksums:     meta.Capabilities.Checksums,
				Registries:    meta.Capabilities.Registries,
				Assets:        meta.Capabilities.Assets,
			}
			if len(caps.Assets) == 0 && len(meta.Assets) > 0 {
				caps.Assets = append([]string{}, meta.Assets...)
			}
			report.Observed.Capabilities = caps
		}
		var policyHashMatches bool
		actual.AssetsComplete, actual.ChecksumsVerified, policyHashMatches = assetState(p, report.Observed.Capabilities, rel.Assets, meta, client, ctx, repo)
		report.Context.PolicyHashMatches = policyHashMatches

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
func providerState(ctx context.Context, client *github.Bound, repo, version string) (*github.PullRequest, State, error) {
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

// releaseMetadata is the RELEASE-METADATA.json contract recorded at publish time.
type releaseMetadata struct {
	Assets     []string          `json:"assets"`
	AssetSHA   map[string]string `json:"asset_sha256"`
	PolicyHash string            `json:"policy_hash"`
	CommitSHA  string            `json:"commit_sha"`
	// Capabilities is the contract that was in force when this version was
	// published. A historical release is judged against it, never against a
	// policy that changed afterwards.
	Capabilities *metadataCapabilities `json:"capabilities,omitempty"`
}

type metadataCapabilities struct {
	GitHubRelease bool     `json:"github_release"`
	Binaries      bool     `json:"binaries"`
	Checksums     bool     `json:"checksums"`
	Registries    []string `json:"registries,omitempty"`
	Assets        []string `json:"required_assets,omitempty"`
}

// readReleaseMetadata downloads and parses the release's own contract. A missing
// or unparsable metadata asset yields a zero value, which callers treat as
// "fall back to the current policy".
func readReleaseMetadata(ctx context.Context, client *github.Bound, repo string, assets []releaseAsset) releaseMetadata {
	for _, a := range assets {
		if a.Name != "RELEASE-METADATA.json" {
			continue
		}
		text, found, err := client.ReleaseAssetText(ctx, repo, a.ID)
		if err != nil || !found {
			return releaseMetadata{}
		}
		var meta releaseMetadata
		if json.Unmarshal([]byte(text), &meta) != nil {
			return releaseMetadata{}
		}
		return meta
	}
	return releaseMetadata{}
}

// assetState evaluates the contract recorded inside the release itself before
// falling back to the current policy. A release published under an older policy
// must not be judged against a policy that changed afterwards: newly required
// assets would otherwise mark every historical release incomplete and block all
// future versions.
func assetState(p *policy.Policy, caps policy.Capabilities, assets []releaseAsset, meta releaseMetadata, client *github.Bound, ctx context.Context, repo string) (complete, sumsVerified, policyHashMatches bool) {
	byName := map[string]releaseAsset{}
	for _, a := range assets {
		byName[a.Name] = a
	}
	// The contract is the capability-derived asset set. A repository with no
	// required assets (source-only, registry-only) only records its metadata.
	contract := append([]string{}, caps.Assets...)
	required := append([]string{}, contract...)
	if caps.GitHubRelease {
		required = append(required, "RELEASE-METADATA.json")
	}
	checksumsEnabled := caps.Checksums
	if checksumsEnabled {
		required = append(required, "SHA256SUMS")
	}
	policyHashMatches = true

	if len(meta.Assets) > 0 && meta.Capabilities == nil {
		// Metadata predating explicit capabilities: its asset list was the contract.
		if meta.PolicyHash != "" && p.Hash != "" {
			policyHashMatches = meta.PolicyHash == p.Hash
		}
		contract = append([]string{}, meta.Assets...)
		required = append([]string{}, contract...)
		if caps.GitHubRelease {
			required = append(required, "RELEASE-METADATA.json")
		}
		if _, hasSums := byName["SHA256SUMS"]; hasSums {
			required = append(required, "SHA256SUMS")
		}
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
	_, hasSums := byName["SHA256SUMS"]
	if !checksumsEnabled && !hasSums {
		return complete, complete, policyHashMatches
	}
	sumsAsset, ok := byName["SHA256SUMS"]
	if !ok {
		return complete, false, policyHashMatches
	}
	text, found, err := client.ReleaseAssetText(ctx, repo, sumsAsset.ID)
	if err != nil || !found {
		return complete, false, policyHashMatches
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			listed[strings.TrimPrefix(fields[1], "*")] = true
		}
	}
	// Coverage is judged against the same contract used for the asset gate:
	// the release's own recorded list when available, else the current policy.
	covers := true
	for _, pattern := range contract {
		if pattern == "SHA256SUMS" || pattern == "RELEASE-METADATA.json" {
			continue
		}
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
	return complete, complete && covers, policyHashMatches
}

func registryState(ctx context.Context, client *github.Bound, verifier *registry.Verifier, p *policy.Policy, repo, version string) (noneRequired, healthy bool) {
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
func Acknowledge(ctx context.Context, client *github.Bound, report *Report, dryRun bool) ([]LabelMutation, error) {
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

// ScanResult is the fleet-wide outcome of a provider scan. It is pure data:
// the scope-bound client that produced it stays in the calling layer.
type ScanResult struct {
	Reports   []Report                `json:"reports"`
	Errors    []string                `json:"errors,omitempty"`
	Execution domain.ExecutionContext `json:"execution"`
}

// Waive records an explicit, auditable human decision that a historical version
// must not be repaired retroactively and must not block newer versions. It only
// adds a label; it never fabricates a release or moves a tag.
func Waive(ctx context.Context, client *github.Bound, repo string, version string) (int, error) {
	prs, err := client.MergedPullRequests(ctx, repo)
	if err != nil {
		return 0, err
	}
	for i := range prs {
		if m := releasePRPattern.FindStringSubmatch(prs[i].Title); m != nil && m[1] == version {
			if err := client.AddIssueLabels(ctx, repo, prs[i].Number, []string{labelWaived}); err != nil {
				return prs[i].Number, err
			}
			return prs[i].Number, nil
		}
	}
	return 0, fmt.Errorf("no merged release PR found for version %s", version)
}

// RepairPlan decides how an incomplete version is recovered. Same-version
// recovery is the only allowed path: starting a newer version to escape an
// unfinished release is exactly the failure mode this layer exists to prevent.
func RepairPlan(r *Report) (allowed bool, reason string, inputs map[string]string) {
	v := r.Verdict
	switch {
	case v.HardFail:
		return false, "TAG_CONFLICT: an existing tag points at the wrong commit; manual intervention required", nil
	case v.Waived:
		return false, "version is explicitly waived; nothing to repair", nil
	case v.Health == domain.HealthHealthy:
		return false, "release transaction is already healthy", nil
	case !v.RepairSameVersion:
		return false, "release is not in a repairable state (" + string(v.Drift) + ")", nil
	case r.Context.Version == "":
		return false, "no version resolved for repair", nil
	}
	return true, "resume the same version through the repository's own release pipeline", map[string]string{
		"version": string(r.Context.Version),
		"force":   "true",
		"repair":  "true",
	}
}

// Repair re-enters the repository's release pipeline for the same version.
// dryRun only reports the dispatch it would perform.
func Repair(ctx context.Context, client *github.Bound, r *Report, workflowFile string, dryRun bool) (map[string]string, error) {
	allowed, reason, inputs := RepairPlan(r)
	if !allowed {
		return nil, fmt.Errorf("repair refused: %s", reason)
	}
	if workflowFile == "" {
		workflowFile = "release.yml"
	}
	if dryRun {
		return inputs, nil
	}
	ref, err := client.DefaultBranch(ctx, r.Context.Repository)
	if err != nil {
		return nil, err
	}
	if err := client.DispatchWorkflow(ctx, r.Context.Repository, workflowFile, ref, inputs); err != nil {
		return nil, err
	}
	return inputs, nil
}
