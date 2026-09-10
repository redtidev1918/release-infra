package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redtidev1918/releasegraph/internal/domain"
	"github.com/redtidev1918/releasegraph/internal/github"
	"github.com/redtidev1918/releasegraph/internal/policy"
	"github.com/redtidev1918/releasegraph/internal/registry"
)

const acme = "acme/app"

func testPolicy() *policy.Policy {
	return &policy.Policy{
		Kind:       "binary",
		Versioning: policy.Versioning{Mode: "release-please"},
		Assets:     policy.Assets{Required: []string{"app-linux", "app-macos"}},
		Registries: map[string]policy.Registry{"github": {Required: true}},
		Checksums:  true,
	}
}

// fakeGitHub serves the endpoints provider.Inspect calls.
type fakeGitHub struct {
	prLabels       []string
	release        bool
	sums           string
	asset404       bool
	failLabel      bool
	metadataCommit string
	mutations      []string
	dispatches     []string
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/"+acme+"/pulls", func(w http.ResponseWriter, r *http.Request) {
		labels := ""
		for _, l := range f.prLabels {
			labels += fmt.Sprintf(`{"name":%q},`, l)
		}
		fmt.Fprintf(w, `[{"number":30,"title":"chore(main): release 2.16.0","state":"closed","merged_at":"2026-09-10T08:00:00Z","merge_commit_sha":"b6c2","labels":[%s]}]`, strings.TrimSuffix(labels, ","))
	})

	mux.HandleFunc("/repos/"+acme+"/git/ref/tags/v2.16.0", func(w http.ResponseWriter, r *http.Request) {
		// lightweight-style ref pointing straight at the commit (tests peeling path separately).
		fmt.Fprint(w, `{"object":{"type":"commit","sha":"b6c2"}}`)
	})

	mux.HandleFunc("/repos/"+acme+"/releases/tags/v2.16.0", func(w http.ResponseWriter, r *http.Request) {
		if !f.release {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"tag_name":"v2.16.0","draft":false,"prerelease":false,
		"assets":[{"id":1,"name":"app-linux","size":10},{"id":2,"name":"app-macos","size":10},
		{"id":3,"name":"RELEASE-METADATA.json","size":10},{"id":4,"name":"SHA256SUMS","size":10}]}`)
	})

	mux.HandleFunc("/repos/"+acme+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if f.release {
			fmt.Fprint(w, `{"tag_name":"v2.16.0"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/repos/"+acme+"/releases/assets/3", func(w http.ResponseWriter, r *http.Request) {
		if f.metadataCommit == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"assets":["app-linux","app-macos"],"policy_hash":"","commit_sha":%q}`, f.metadataCommit)
	})

	mux.HandleFunc("/repos/"+acme+"/releases/assets/4", func(w http.ResponseWriter, r *http.Request) {
		if f.asset404 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(f.sums))
	})

	mux.HandleFunc("/repos/"+acme+"/actions/workflows/release.yml/dispatches", func(w http.ResponseWriter, r *http.Request) {
		f.dispatches = append(f.dispatches, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/repos/"+acme, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"default_branch":"main"}`)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			f.mutations = append(f.mutations, r.Method+" "+r.URL.Path)
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/labels") {
			if f.failLabel {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func newTestServer(t *testing.T, f *fakeGitHub) string {
	t.Helper()
	server := httptest.NewServer(f.handler())
	t.Cleanup(server.Close)
	return server.URL
}

func runInspect(t *testing.T, f *fakeGitHub) (*Report, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(f.handler())
	t.Cleanup(server.Close)
	client := github.NewForTest(server.URL)
	verifier := registry.New()
	report, err := Inspect(context.Background(), client, verifier, testPolicy(), acme, "2.16.0")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return report, server
}

// Regression fixture for TelePost 2.16.0: actual release fully healthy but the
// merged release PR still carries autorelease: pending.
func TestInspectDetectsACKMissing(t *testing.T) {
	f := &fakeGitHub{
		prLabels: []string{labelPending},
		release:  true,
		sums:     "aaaa  app-linux\nbbbb  app-macos\n",
	}
	report, _ := runInspect(t, f)

	if report.Verdict.Drift != DriftACKMissing || report.Verdict.Health != domain.HealthACKPending || !report.Verdict.ACKAllowed {
		t.Fatalf("verdict = %+v, want ACK_MISSING/ACK_PENDING/allowed", report.Verdict)
	}
	if report.Context.ReleasePR != 30 || report.Context.PRMergeSHA != "b6c2" {
		t.Fatalf("context = %+v", report.Context)
	}
	if !report.Observed.Actual.Healthy() {
		t.Fatalf("actual should be healthy: %+v", report.Observed.Actual)
	}
}

func TestInspectInSyncWhenTagged(t *testing.T) {
	f := &fakeGitHub{
		prLabels: []string{labelTagged},
		release:  true,
		sums:     "aaaa  app-linux\nbbbb  app-macos\n",
	}
	report, _ := runInspect(t, f)
	if report.Verdict.Drift != DriftInSync || report.Verdict.ACKAllowed {
		t.Fatalf("verdict = %+v, want IN_SYNC with no ACK", report.Verdict)
	}
}

func TestInspectRecoverableWhenReleaseMissing(t *testing.T) {
	f := &fakeGitHub{prLabels: []string{labelPending}, release: false}
	report, _ := runInspect(t, f)
	if report.Verdict.Drift != DriftReleaseMissing || report.Verdict.ACKAllowed {
		t.Fatalf("verdict = %+v, want RELEASE_MISSING with no ACK", report.Verdict)
	}
}

func TestInspectFalseACK(t *testing.T) {
	f := &fakeGitHub{
		prLabels: []string{labelTagged},
		release:  true,
		sums:     "aaaa  app-linux\n", // app-macos not covered
	}
	report, _ := runInspect(t, f)
	if report.Verdict.Drift != DriftFalseACK {
		t.Fatalf("drift = %s, want FALSE_ACK", report.Verdict.Drift)
	}
}

func TestInspectTagConflictHardFails(t *testing.T) {
	f := &fakeGitHub{
		prLabels: []string{labelPending},
		release:  true,
		sums:     "aaaa  app-linux\nbbbb  app-macos\n",
	}
	server := httptest.NewServer(f.handler())
	defer server.Close()
	// Override the tag ref to point to a different commit.
	client := github.NewForTest(server.URL)
	// Use a dedicated mux to answer the wrong commit without touching shared state.
	_ = client
	report, _ := runInspect(t, f)
	// Baseline is correct; now force mismatch by changing fake ref handler result:
	// re-run with a wrapper server that rewrites the tag ref.
	if report.Verdict.Drift != DriftACKMissing {
		t.Fatalf("baseline broken: %s", report.Verdict.Drift)
	}

	bad := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/git/ref/tags/v2.16.0") {
			fmt.Fprint(w, `{"object":{"type":"commit","sha":"WRONG"}}`)
			return
		}
		f.handler().ServeHTTP(w, r)
	})
	server2 := httptest.NewServer(bad)
	defer server2.Close()
	badReport, err := Inspect(context.Background(), github.NewForTest(server2.URL), registry.New(), testPolicy(), acme, "2.16.0")
	if err != nil {
		t.Fatal(err)
	}
	if !badReport.Verdict.HardFail || badReport.Verdict.Drift != DriftTagConflict {
		t.Fatalf("verdict = %+v, want TAG_CONFLICT hard fail", badReport.Verdict)
	}
}

func TestAcknowledgeAppliesIdempotentLabelPlan(t *testing.T) {
	f := &fakeGitHub{
		prLabels: []string{labelPending},
		release:  true,
		sums:     "aaaa  app-linux\nbbbb  app-macos\n",
	}
	report, server := runInspect(t, f)
	defer server.Close()
	client := github.NewForTest(server.URL)

	// Dry run: no mutations.
	planned, err := Acknowledge(context.Background(), client, report, true)
	if err != nil || len(planned) != 2 {
		t.Fatalf("dry-run plan = %v, %v", planned, err)
	}
	if len(f.mutations) != 0 {
		t.Fatalf("dry run mutated: %v", f.mutations)
	}

	// Real ACK: add tagged, remove pending.
	if _, err := Acknowledge(context.Background(), client, report, false); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	joined := strings.Join(f.mutations, ";")
	if !strings.Contains(joined, "POST") || !strings.Contains(joined, "DELETE") ||
		!strings.Contains(joined, "/issues/30/labels") {
		t.Fatalf("mutations = %v", f.mutations)
	}
}

func TestAcknowledgeRefusesIncomplete(t *testing.T) {
	f := &fakeGitHub{prLabels: []string{labelPending}, release: false}
	report, server := runInspect(t, f)
	defer server.Close()
	if _, err := Acknowledge(context.Background(), github.NewForTest(server.URL), report, false); err == nil {
		t.Fatal("ACK must be refused for an incomplete release")
	}
}

// Acceptance: a transient provider API failure must never be treated as a
// reason to create a new version; the same version is ACKed on the next run.
func TestAcknowledgeRetriesOnNextRunAfterTransientFailure(t *testing.T) {
	f := &fakeGitHub{
		prLabels:  []string{labelPending},
		release:   true,
		sums:      "aaaa  app-linux\nbbbb  app-macos\n",
		failLabel: true,
	}
	report, server := runInspect(t, f)
	defer server.Close()
	client := github.NewForTest(server.URL)

	if _, err := Acknowledge(context.Background(), client, report, false); err == nil {
		t.Fatal("a failing label API must surface as an error, not a silent success")
	}
	if len(f.mutations) == 0 {
		// The client retries internally; ensure the attempt was actually made.
		t.Fatal("no mutation attempted")
	}

	// The provider state is unchanged (still pending); the next run repairs it.
	f.failLabel = false
	f.mutations = nil
	again, err := Inspect(context.Background(), client, registry.New(), testPolicy(), acme, "2.16.0")
	if err != nil {
		t.Fatal(err)
	}
	if again.Verdict.Drift != DriftACKMissing {
		t.Fatalf("drift after failure = %s, want ACK_MISSING (no new version)", again.Verdict.Drift)
	}
	if _, err := Acknowledge(context.Background(), client, again, false); err != nil {
		t.Fatalf("retry ACK: %v", err)
	}
	if len(f.mutations) == 0 {
		t.Fatal("retry did not apply the ACK")
	}
}

func TestRepairPlanOnlyResumesSameVersion(t *testing.T) {
	// Incomplete release: allowed, and it must target the same version.
	incomplete := obs(StatePending)
	incomplete.Actual.ReleaseExists = false
	report := &Report{Observed: incomplete, Verdict: Classify(incomplete)}
	report.Context = Context{Repository: acme, Version: "2.16.0"}
	allowed, reason, inputs := RepairPlan(report)
	if !allowed {
		t.Fatalf("recoverable version refused repair: %s", reason)
	}
	if inputs["version"] != "2.16.0" || inputs["repair"] != "true" || inputs["force"] != "true" {
		t.Fatalf("repair inputs = %v", inputs)
	}

	// Healthy: nothing to do.
	healthy := &Report{Observed: obs(StateTagged), Verdict: Classify(obs(StateTagged))}
	if allowed, _, _ := RepairPlan(healthy); allowed {
		t.Fatal("healthy release must not be repaired")
	}

	// Waived: explicitly accepted, never repaired.
	waivedObs := obs(StateTagged)
	waivedObs.Actual.Waived = true
	waived := &Report{Observed: waivedObs, Verdict: Classify(waivedObs)}
	if allowed, _, _ := RepairPlan(waived); allowed {
		t.Fatal("waived version must not be repaired")
	}

	// Tag conflict: history rewrite is never the repair.
	conflictObs := obs(StatePending)
	conflictObs.Actual.TagCommit = "dead"
	conflict := &Report{Observed: conflictObs, Verdict: Classify(conflictObs)}
	if allowed, reason, _ := RepairPlan(conflict); allowed || !strings.Contains(reason, "TAG_CONFLICT") {
		t.Fatalf("tag conflict must hard-refuse repair: %v %s", allowed, reason)
	}
}

func TestRepairDispatchesSameVersionThroughRepoPipeline(t *testing.T) {
	f := &fakeGitHub{prLabels: []string{labelPending}, release: false}
	server := httptest.NewServer(f.handler())
	defer server.Close()
	client := github.NewForTest(server.URL)
	report, err := Inspect(context.Background(), client, registry.New(), testPolicy(), acme, "2.16.0")
	if err != nil {
		t.Fatal(err)
	}

	// Dry run only reports the plan.
	planned, err := Repair(context.Background(), client, report, "", true)
	if err != nil || planned["version"] != "2.16.0" {
		t.Fatalf("dry-run plan = %v %v", planned, err)
	}
	if len(f.dispatches) != 0 {
		t.Fatalf("dry run dispatched: %v", f.dispatches)
	}

	// Applying dispatches the repository's own release workflow on its default branch.
	applied, err := Repair(context.Background(), client, report, "", false)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if applied["repair"] != "true" {
		t.Fatalf("inputs = %v", applied)
	}
	if len(f.dispatches) != 1 || !strings.Contains(f.dispatches[0], "actions/workflows/release.yml/dispatches") {
		t.Fatalf("dispatches = %v", f.dispatches)
	}
}

// Manual / tag providers have no release PR, so the expected tag target comes
// from the release's own metadata. Without it every healthy manual release was
// reported RECOVERABLE forever.
func TestManualProviderUsesMetadataCommitAsExpectedTagTarget(t *testing.T) {
	f := &fakeGitHub{
		release:        true,
		metadataCommit: "b6c2",
		sums:           "aaaa  app-linux\nbbbb  app-macos\n",
	}
	manual := testPolicy()
	manual.Versioning.Mode = "manual"
	server := httptest.NewServer(f.handler())
	defer server.Close()
	report, err := Inspect(context.Background(), github.NewForTest(server.URL), registry.New(), manual, acme, "2.16.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Drift != DriftInSync || report.Verdict.Health != domain.HealthHealthy {
		t.Fatalf("manual provider verdict = %+v (expected HEALTHY/IN_SYNC)", report.Verdict)
	}
}
