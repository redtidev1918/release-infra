package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/redtidev1918/releasegraph/internal/credential"
	"github.com/redtidev1918/releasegraph/internal/domain"
	"github.com/redtidev1918/releasegraph/internal/fleet"
	"github.com/redtidev1918/releasegraph/internal/github"
	"github.com/redtidev1918/releasegraph/internal/rollout"
)

// rolloutCommand implements:
//
//	releasegraph rollout status [--version vX.Y.Z]      # current pins + canary
//	releasegraph rollout plan   --version vX.Y.Z        # side-effect free
//	releasegraph rollout apply  --version vX.Y.Z --apply # opens upgrade PRs
//
// Rollout is a fleet (control plane) operation: it needs
// RELEASEGRAPH_FLEET_TOKEN and reads its repository list from fleet.yaml.
func rolloutCommand(w io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: releasegraph rollout [status|plan|apply]")
	}
	sub := args[0]
	var format, manifestPath, version, releaseRepo string
	var apply bool
	fs := flags(&format)
	fs.StringVar(&manifestPath, "manifest", "fleet.yaml", "fleet manifest")
	fs.StringVar(&version, "version", "", "target ReleaseGraph version, e.g. v1.4.1")
	fs.StringVar(&releaseRepo, "release-repo", "redtidev1918/releasegraph", "repository whose releases define versions")
	fs.BoolVar(&apply, "apply", false, "open upgrade pull requests (default: plan only)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if sub != "status" && sub != "plan" && sub != "apply" {
		return fmt.Errorf("unknown rollout subcommand %q", sub)
	}

	ctx := context.Background()
	if err := credential.RequireFleet("rollout " + sub); err != nil {
		return err
	}
	if err := credential.UseFleetCredential(); err != nil {
		return err
	}
	client := github.New().Bind(domain.ExecutionContext{
		Scope:           domain.ScopeFleet,
		Actor:           actorName(),
		CredentialClass: domain.CredentialFleet,
	})

	manifest, err := fleet.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	if version == "" {
		if sub == "apply" {
			return fmt.Errorf("rollout apply requires --version")
		}
		version, err = latestStableVersion(ctx, client, releaseRepo)
		if err != nil {
			return err
		}
	}
	if !rollout.MutableChannel.MatchString(version) && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	managed, _ := resolveManaged(ctx, client, manifest)
	entries := rollout.InspectPins(ctx, client, manifest, managed, version)
	canary, _ := manifest.Canary()
	evidence := canaryEvidence(ctx, client, canary, version)
	plan := rollout.BuildPlan(manifest, entries, version, canary, evidence)

	if sub == "plan" || sub == "status" {
		if format == "json" {
			return write(w, "json", plan, nil)
		}
		humanRolloutPlan(w, plan)
		return nil
	}

	// apply
	if !apply {
		if format == "json" {
			return write(w, "json", map[string]any{"mode": "dry-run", "plan": plan}, nil)
		}
		humanRolloutPlan(w, plan)
		fmt.Fprintln(w, "\ndry-run only; pass --apply to open upgrade pull requests")
		return nil
	}
	return rolloutApply(ctx, w, format, client, plan, version)
}

func humanRolloutPlan(w io.Writer, plan rollout.Plan) {
	fmt.Fprintf(w, "target: %s\n", plan.Target)
	if plan.Canary != "" {
		state := "NOT PASSED"
		if plan.CanaryPassed {
			state = "passed"
		}
		fmt.Fprintf(w, "canary: %s (%s", plan.Canary, state)
		if plan.CanaryReason != "" {
			fmt.Fprintf(w, " — %s", plan.CanaryReason)
		}
		fmt.Fprintln(w, ")")
	}
	for _, entry := range plan.Entries {
		marker := ""
		if entry.Canary {
			marker = " [canary]"
		}
		mutable := ""
		if entry.Current.Mutable {
			mutable = " (mutable channel alias)"
		}
		current := entry.Current.Ref
		if current == "" {
			current = "none"
		}
		fmt.Fprintf(w, "  %-42s %-9s %s -> %s%s%s\n", entry.Repository, entry.Status, current, entry.Target, marker, mutable)
	}
	fmt.Fprintf(w, "\nready: %d  blocked: %d\n", len(plan.Ready), len(plan.Blocked))
}

func rolloutApply(ctx context.Context, w io.Writer, format string, client *github.Bound, plan rollout.Plan, version string) error {
	results := []map[string]string{}
	for _, entry := range plan.Entries {
		if entry.Status != rollout.StatusReady {
			continue
		}
		pr, err := client.OpenChangePR(ctx, github.FileUpdate{
			Repo:    entry.Repository,
			Path:    rollout.WorkflowPath,
			Branch:  "chore/releasegraph-" + version,
			Message: "chore(release-infra): bump ReleaseGraph to " + version,
			Title:   "chore(release-infra): bump ReleaseGraph to " + version,
			Body: "Pins the release infrastructure to the immutable version `" + version + "`.\n\n" +
				"Opened by `releasegraph rollout` after the canary repository completed a release lifecycle on this version.\n" +
				"Reverting this pull request returns the repository to its previous pin.",
			Transform: func(current string) (string, error) { return rollout.Repin(current, version) },
		})
		item := map[string]string{"repository": entry.Repository, "target": version}
		if err != nil {
			item["status"] = "failed"
			item["error"] = err.Error()
		} else {
			item["status"] = "pr-opened"
			item["pullRequest"] = pr
		}
		results = append(results, item)
	}
	if format == "json" {
		return write(w, "json", map[string]any{"mode": "apply", "target": version, "results": results}, nil)
	}
	for _, item := range results {
		if item["status"] == "pr-opened" {
			fmt.Fprintf(w, "opened  %s -> %s %s\n", item["repository"], version, item["pullRequest"])
		} else {
			fmt.Fprintf(w, "failed  %s: %s\n", item["repository"], item["error"])
		}
	}
	return nil
}

func resolveManaged(ctx context.Context, client *github.Bound, manifest *fleet.Manifest) ([]fleet.Resolved, []fleet.Resolved) {
	owner := ""
	if names := manifest.EnabledNames(); len(names) > 0 {
		owner, _, _ = strings.Cut(names[0], "/")
	}
	if owner == "" {
		return fleet.Resolve(manifest, nil)
	}
	discovered, err := fleet.Discover(ctx, client, owner, false)
	if err != nil {
		return fleet.Resolve(manifest, nil)
	}
	return fleet.Resolve(manifest, discovered.Repositories)
}

func canaryEvidence(ctx context.Context, client *github.Bound, canary, version string) rollout.CanaryEvidence {
	if canary == "" {
		return rollout.CanaryEvidence{Reason: "no canary declared in fleet.yaml"}
	}
	raw, found, err := client.ReadFile(ctx, canary, rollout.WorkflowPath, "")
	if err != nil || !found {
		return rollout.CanaryEvidence{Reason: "cannot read " + canary + " " + rollout.WorkflowPath}
	}
	pin := rollout.ParsePin(raw)
	if pin.Ref != version {
		return rollout.CanaryEvidence{Reason: canary + " is pinned to " + pin.Ref + ", not " + version}
	}
	run, ok, err := client.LatestWorkflowRun(ctx, canary, "release.yml")
	if err != nil || !ok {
		return rollout.CanaryEvidence{Pinned: true, Reason: "no release workflow run found for " + canary}
	}
	if run.Conclusion != "success" {
		return rollout.CanaryEvidence{Pinned: true, Reason: "canary run " + run.HTMLURL + " concluded " + run.Conclusion}
	}
	return rollout.CanaryEvidence{Pinned: true, Lifecycle: true, Reason: "canary run succeeded on " + version + ": " + run.HTMLURL}
}

func latestStableVersion(ctx context.Context, client *github.Bound, repo string) (string, error) {
	var release struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if _, err := client.GetOptional(ctx, fmt.Sprintf("repos/%s/releases/latest", repo), &release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", fmt.Errorf("no latest release found for %s", repo)
	}
	return release.TagName, nil
}
