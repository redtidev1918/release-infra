package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	rgerrors "github.com/redtidev1918/release-infra/internal/errors"
	"github.com/redtidev1918/release-infra/internal/policy"
)

func TestVerifyPublishedVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/1.2.3":
			w.WriteHeader(http.StatusOK)
		case "/app/1.2.4":
			w.WriteHeader(http.StatusNotFound)
		case "/pypi/app/2.0.0/json", "/pypi/devart-dl/4.1.1/json":
			w.WriteHeader(http.StatusOK)
		case "/pub/app":
			_, _ = w.Write([]byte(`{"versions":{"3.0.0":{}}}`))
		case "/token":
			_, _ = w.Write([]byte(`{"token":"secret"}`))
		case "/v2/acme/app/manifests/4.0.0":
			if r.Header.Get("Authorization") != "Bearer secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	v := NewForTest(server.URL)
	ctx := context.Background()
	if err := v.Verify(ctx, "npm", policy.Registry{}, "Acme/app", "1.2.3", nil); err != nil {
		t.Fatal(err)
	}
	err := v.Verify(ctx, "npm", policy.Registry{}, "Acme/app", "1.2.4", nil)
	if !rgerrors.IsKind(err, rgerrors.RegistryConflict) {
		t.Fatalf("missing npm version: %v", err)
	}
	if err := v.Verify(ctx, "pypi", policy.Registry{}, "acme/app", "2.0.0", []byte("[project]\nname=\"app\"")); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(ctx, "pypi", policy.Registry{}, "acme/deviantart-downloader", "4.1.1", []byte("[project]\nname=\"devart-dl\"")); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(ctx, "pub", policy.Registry{}, "acme/app", "3.0.0", nil); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(ctx, "ghcr", policy.Registry{Image: "ghcr.io/acme/app"}, "acme/app", "4.0.0", nil); err != nil {
		t.Fatal(err)
	}
}
