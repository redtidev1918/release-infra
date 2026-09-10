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
	prLabels  []string
	release   bool
	sums      string
	asset404  bool
	mutations []string
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

	mux.HandleFunc("/repos/"+acme+"/releases/assets/4", func(w http.ResponseWriter, r *http.Request) {
		if f.asset404 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(f.sums))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			f.mutations = append(f.mutations, r.Method+" "+r.URL.Path)
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/labels") {
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
