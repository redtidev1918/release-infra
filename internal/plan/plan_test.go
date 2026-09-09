package plan

import (
	"testing"

	"github.com/redtidev1918/release-infra/internal/domain"
	"github.com/redtidev1918/release-infra/internal/graph"
)

func TestReconcileEventDoesNotBumpHealthyNode(t *testing.T) {
	g, err := graph.Load("../../testdata/graph/diamond.yml")
	if err != nil {
		t.Fatal(err)
	}
	health := map[string]domain.Health{"core": domain.HealthHealthy}
	first, err := Evaluate(g, health)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluate(g, health)
	if err != nil {
		t.Fatal(err)
	}
	if first.Noop[0] != "core" || len(first.Ready) != 2 || first.Blocked["deploy"] == nil {
		t.Fatalf("plan=%+v", first)
	}
	if len(first.Ready) != len(second.Ready) || first.Ready[0] != second.Ready[0] {
		t.Fatalf("reconcile is not idempotent: %+v vs %+v", first, second)
	}
}
