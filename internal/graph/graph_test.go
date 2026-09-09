package graph

import (
	"strings"
	"testing"
)

func TestDiamondOrderAndReadySet(t *testing.T) {
	g, err := Load("../../testdata/graph/diamond.yml")
	if err != nil {
		t.Fatal(err)
	}
	order, err := TopSort(g)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"core", "cli", "web", "deploy"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order=%v", order)
	}
	view, err := BuildView(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Ready) != 1 || view.Ready[0] != "core" {
		t.Fatalf("ready=%v", view.Ready)
	}
	if got := view.Blocked["deploy"]; len(got) != 2 {
		t.Fatalf("deploy blocked by %v", got)
	}
}

func TestCycleRejected(t *testing.T) {
	if _, err := Load("../../testdata/graph/cycle.yml"); err == nil || !strings.Contains(err.Error(), "cycle: a -> c -> b -> a") {
		t.Fatalf("err=%v", err)
	}
}

func TestMissingDependencyRejected(t *testing.T) {
	if _, err := Load("../../testdata/graph/missing.yml"); err == nil || !strings.Contains(err.Error(), "missing project") {
		t.Fatalf("err=%v", err)
	}
}

func TestMermaid(t *testing.T) {
	g, err := Load("../../testdata/graph/diamond.yml")
	if err != nil {
		t.Fatal(err)
	}
	text, err := Mermaid(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range []string{"core --> cli", "core --> web", "cli --> deploy", "web --> deploy"} {
		if !strings.Contains(text, edge) {
			t.Fatalf("missing %s in\n%s", edge, text)
		}
	}
}
