package reconcile

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/redtidev1918/releasegraph/internal/domain"
	"github.com/redtidev1918/releasegraph/internal/github"
)

func TestInspectUsesRemoteStateAndIsIdempotent(t *testing.T) {
	policy := func(version string, assets string) string {
		return fmt.Sprintf(`{"kind":"binary","versioning":{"mode":"manual","version":%q},"assets":{"required":%s},"registries":{"github":{"required":true}},"checksums":false}`, version, assets)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/core/contents/.release-policy.yml":
			writeContent(w, policy("1.0.0", `["app"]`))
		case "/repos/acme/web/contents/.release-policy.yml":
			writeContent(w, policy("2.0.0", `[]`))
		case "/repos/acme/core/releases/tags/v1.0.0":
			fmt.Fprint(w, `{"tag_name":"v1.0.0","draft":false,"prerelease":false,"target_commitish":"abc","assets":[{"name":"app","size":2},{"name":"RELEASE-METADATA.json","size":2}]}`)
		case "/repos/acme/core/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v1.0.0"}`)
		case "/repos/acme/web/releases/tags/v2.0.0":
			w.WriteHeader(http.StatusNotFound)
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	g := &domain.ReleaseGraph{APIVersion: "releasegraph.dev/v1", Projects: map[string]domain.Project{
		"core": {Repo: domain.Repository{Owner: "acme", Name: "core"}},
		"web":  {Repo: domain.Repository{Owner: "acme", Name: "web"}, DependsOn: []domain.Dependency{{ID: "core", Condition: domain.ConditionHealthy}}},
	}}
	client := github.NewForTest(server.URL)
	first, err := Inspect(context.Background(), client, g)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inspect(context.Background(), client, g)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reconcile is not idempotent:\n%+v\n%+v", first, second)
	}
	if !reflect.DeepEqual(first.Ready, []string{"web"}) || !reflect.DeepEqual(first.Noop, []string{"core"}) || len(first.Blocked) != 0 {
		t.Fatalf("plan=%+v", first)
	}
}

func writeContent(w http.ResponseWriter, content string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	fmt.Fprintf(w, `{"encoding":"base64","content":%q}`, encoded)
}
