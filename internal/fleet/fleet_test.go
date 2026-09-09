package fleet

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redtidev1918/release-infra/internal/domain"
	"github.com/redtidev1918/release-infra/internal/github"
)

func TestDiscoverOwnerAgnosticFleet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/users/acme/repos") {
			_, _ = w.Write([]byte(`[
				{"full_name":"acme/app","visibility":"public","default_branch":"main","fork":false,"archived":false},
				{"full_name":"acme/fork","visibility":"public","default_branch":"main","fork":true,"archived":false}
			]`))
			return
		}
		if r.URL.Path == "/repos/acme/app/contents/.release-policy.yml" {
			content := base64.StdEncoding.EncodeToString([]byte(`{"kind":"binary","versioning":{"mode":"manual","version":"1.0.0"}}`))
			_, _ = w.Write([]byte(`{"encoding":"base64","content":"` + content + `"}`))
			return
		}
		if r.URL.Path == "/repos/acme/app/releases" {
			_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0","draft":false,"prerelease":false,"assets":[{"name":"app.zip"}]}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	out, err := Discover(context.Background(), github.NewForTest(server.URL), "acme", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repositories) != 2 {
		t.Fatalf("repos=%+v", out.Repositories)
	}
	if !out.Repositories[0].Managed || out.Repositories[0].Health != domain.HealthNeedsReview || out.Repositories[0].LatestRelease != "v1.0.0" {
		t.Fatalf("app=%+v", out.Repositories[0])
	}
	if out.Repositories[1].Classification != "fork" {
		t.Fatalf("fork=%+v", out.Repositories[1])
	}
}
