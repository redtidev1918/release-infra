package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/redtidev1918/releasegraph/internal/fleet"
	"github.com/redtidev1918/releasegraph/internal/github"
	"github.com/redtidev1918/releasegraph/internal/policy"
	"github.com/redtidev1918/releasegraph/internal/provider"
	"github.com/redtidev1918/releasegraph/internal/registry"
)

// providerCommand implements:
//
//	releasegraph provider inspect   --repo owner/name [--version X]
//	releasegraph provider reconcile --repo owner/name [--version X] [--apply]
//	releasegraph provider reconcile --all --owner someone [--apply]
type providerOptions struct {
	format  string
	repo    string
	version string
	path    string
	owner   string
	all     bool
	apply   bool
}

func providerCommand(w io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: releasegraph provider [inspect|reconcile|acknowledge]")
	}
	sub := args[0]
	opts := providerOptions{}
	fs := flag.NewFlagSet("provider "+sub, flag.ContinueOnError)
	fs.StringVar(&opts.format, "output", "human", "human or json")
	fs.StringVar(&opts.format, "format", "human", "alias for --output")
	fs.StringVar(&opts.repo, "repo", "", "repository as owner/name")
	fs.StringVar(&opts.version, "version", "", "version to reconcile (defaults to the manifest)")
	fs.StringVar(&opts.path, "path", "", "local policy file to use instead of the target repository's own policy")
	fs.StringVar(&opts.owner, "owner", "", "fleet owner for --all")
	fs.BoolVar(&opts.all, "all", false, "scan every managed repository of --owner")
	fs.BoolVar(&opts.apply, "apply", false, "apply provider mutations (default is a side-effect-free plan)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch sub {
	case "inspect":
		return providerInspect(w, opts)
	case "reconcile", "acknowledge":
		return providerReconcile(w, opts)
	case "waive":
		return providerWaive(w, opts)
	default:
		return fmt.Errorf("unknown provider subcommand %q", sub)
	}
}

// providerWaive records an explicit human decision that a historical version
// will not be repaired and must not block newer versions. It is the only
// provider command that requires an explicit version.
func providerWaive(w io.Writer, opts providerOptions) error {
	if opts.repo == "" || opts.version == "" {
		return fmt.Errorf("provider waive requires --repo and --version")
	}
	number, err := provider.Waive(context.Background(), github.New(), opts.repo, opts.version)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "waived %s %s (release PR #%d) — say why in the PR conversation\n", opts.repo, opts.version, number)
	return nil
}

func providerInspect(w io.Writer, opts providerOptions) error {
	scan, err := scanProvider(context.Background(), opts)
	if err != nil {
		return err
	}
	if opts.format == "json" || opts.format == "" {
		return write(w, "json", scan, nil)
	}
	for _, report := range scan.Reports {
		humanProviderReport(w, report)
	}
	for _, failure := range scan.Errors {
		fmt.Fprintln(w, "ERROR", failure)
	}
	if len(scan.Errors) > 0 {
		return fmt.Errorf("%d repository inspection(s) failed", len(scan.Errors))
	}
	return nil
}

func providerReconcile(w io.Writer, opts providerOptions) error {
	client := github.New()
	scan, err := scanProvider(context.Background(), opts)
	if err != nil {
		return err
	}
	applied := []provider.Report{}
	for _, report := range scan.Reports {
		if !report.Verdict.ACKAllowed {
			continue
		}
		mutations, err := provider.Acknowledge(context.Background(), client, &report, !opts.apply)
		if err != nil {
			scan.Errors = append(scan.Errors, fmt.Sprintf("%s: %v", report.Context.Repository, err))
			continue
		}
		report.PlannedACK = mutations
		applied = append(applied, report)
	}
	out := map[string]any{"mode": mode(opts.apply), "reports": scan.Reports, "acknowledged": applied, "errors": scan.Errors}
	if opts.format == "json" || opts.format == "" {
		return write(w, "json", out, nil)
	}
	for _, report := range scan.Reports {
		humanProviderReport(w, report)
	}
	for _, report := range applied {
		verb := "would acknowledge"
		if opts.apply {
			verb = "acknowledged"
		}
		fmt.Fprintf(w, "  %s %s %s (PR #%d)\n", verb, report.Context.Repository, report.Context.Version, report.Context.ReleasePR)
	}
	for _, failure := range scan.Errors {
		fmt.Fprintln(w, "ERROR", failure)
	}
	return nil
}

func mode(apply bool) string {
	if apply {
		return "apply"
	}
	return "dry-run"
}

// scanProvider collects one report per (repository, version) pair.
func scanProvider(ctx context.Context, opts providerOptions) (*provider.ScanResult, error) {
	client := github.New()
	verifier := registry.New()
	scan := &provider.ScanResult{Reports: []provider.Report{}, Errors: []string{}}

	targets := []string{}
	switch {
	case opts.all:
		if opts.owner == "" {
			return nil, fmt.Errorf("--all requires --owner")
		}
		discovered, err := fleet.Discover(ctx, client, opts.owner, false)
		if err != nil {
			return nil, err
		}
		for _, repo := range discovered.Repositories {
			if repo.Managed && !repo.Archived && !repo.Fork {
				targets = append(targets, repo.Name)
			}
		}
		sort.Strings(targets)
	case opts.repo != "":
		targets = append(targets, opts.repo)
	default:
		return nil, fmt.Errorf("--repo owner/name or --all --owner is required")
	}

	for _, repo := range targets {
		p, err := loadProviderPolicy(ctx, client, repo, opts.path)
		if err != nil {
			scan.Errors = append(scan.Errors, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		version := opts.version
		if version == "" {
			version, err = desiredVersionFor(ctx, client, repo, p)
			if err != nil {
				scan.Errors = append(scan.Errors, fmt.Sprintf("%s: %v", repo, err))
				continue
			}
		}
		report, err := provider.Inspect(ctx, client, verifier, p, repo, version)
		if err != nil {
			scan.Errors = append(scan.Errors, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		scan.Reports = append(scan.Reports, *report)
	}
	return scan, nil
}

func loadProviderPolicy(ctx context.Context, client *github.Client, repo, path string) (*policy.Policy, error) {
	if path != "" {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("policy file %s not found", path)
		}
		return policy.Load(path)
	}
	raw, found, err := client.ReadFile(ctx, repo, ".release-policy.yml", "")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf(".release-policy.yml is missing")
	}
	return policy.Parse(raw)
}

func desiredVersionFor(ctx context.Context, client *github.Client, repo string, p *policy.Policy) (string, error) {
	versioningMode := p.Versioning.Mode
	if versioningMode == "" {
		versioningMode = p.Versioning.Provider
	}
	if versioningMode != "release-please" {
		return policy.DesiredVersion(p, "", ".")
	}
	manifestPath := p.Versioning.Manifest
	if manifestPath == "" {
		manifestPath = ".release-please-manifest.json"
	}
	raw, found, err := client.ReadFile(ctx, repo, manifestPath, "")
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("release manifest %s is missing", manifestPath)
	}
	return policy.DesiredVersionFromManifest(p, raw)
}

// humanProviderReport prints the diagnosis layout used in the operator docs.
func humanProviderReport(w io.Writer, r provider.Report) {
	fmt.Fprintf(w, "%s %s\n", r.Context.Repository, r.Context.Version)
	fmt.Fprintf(w, "  Release PR:  #%d merged=%v\n", r.Context.ReleasePR, r.Context.ReleasePR != 0)
	if r.Context.PRMergeSHA != "" {
		fmt.Fprintf(w, "  Merge commit: %s\n", r.Context.PRMergeSHA)
	}
	fmt.Fprintf(w, "  Tag:         %s correct=%v\n", r.Context.Tag, r.Observed.Actual.TagExists && r.Observed.Actual.TagCommit == r.Observed.Actual.ExpectedCommit)
	fmt.Fprintf(w, "  Release:     exists=%v latest=%v assets=%v checksums=%v\n",
		r.Observed.Actual.ReleaseExists, r.Observed.Actual.Latest, r.Observed.Actual.AssetsComplete, r.Observed.Actual.ChecksumsVerified)
	fmt.Fprintf(w, "  Provider:    %s state=%s\n", r.Observed.Provider, r.Observed.ProviderState)
	fmt.Fprintf(w, "  Diagnosis:   %s\n", r.Verdict.Drift)
	fmt.Fprintf(w, "  Health:      %s\n", r.Verdict.Health)
	fmt.Fprintf(w, "  Repair:      %s\n", repairLabel(r.Verdict))
	if r.Verdict.Waived {
		fmt.Fprintf(w, "  Waived:      yes (historical version accepted as-is)\n")
	}
	for _, m := range r.PlannedACK {
		fmt.Fprintf(w, "  ACK:         %s %s\n", m.Action, m.Label)
	}
	if r.Verdict.Reason != "" {
		fmt.Fprintf(w, "  Reason:      %s\n", r.Verdict.Reason)
	}
}

func repairLabel(v provider.Verdict) string {
	switch {
	case v.HardFail:
		return "unsafe (TAG_CONFLICT: manual intervention required)"
	case v.ACKAllowed:
		return "safe (acknowledge provider)"
	case v.RepairSameVersion:
		return "safe (repair same version first)"
	default:
		return "none"
	}
}
