package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOptionalDistinguishesMissingFromPermissionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client := NewForTest(server.URL)
	if found, err := client.GetOptional(context.Background(), "missing", nil); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	if _, err := client.GetOptional(context.Background(), "forbidden", nil); err == nil {
		t.Fatal("permission failure was hidden as a missing resource")
	}
}
