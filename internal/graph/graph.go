package graph

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/redtidev1918/release-infra/internal/config"
	rgdomain "github.com/redtidev1918/release-infra/internal/domain"
	rgerrors "github.com/redtidev1918/release-infra/internal/errors"
)

type View struct {
	Order      []string            `json:"order"`
	Ready      []string            `json:"ready"`
	Blocked    map[string][]string `json:"blocked"`
	Upstream   map[string][]string `json:"upstream"`
	Downstream map[string][]string `json:"downstream"`
}

func Load(path string) (*rgdomain.ReleaseGraph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, rgerrors.Wrap(rgerrors.Graph, "read release graph", err)
	}
	var g rgdomain.ReleaseGraph
	if err := config.Unmarshal(data, &g); err != nil {
		return nil, rgerrors.Wrap(rgerrors.Graph, "parse release graph YAML", err)
	}
	if err := Validate(&g); err != nil {
		return nil, err
	}
	return &g, nil
}

func Validate(g *rgdomain.ReleaseGraph) error {
	if g.APIVersion != "releasegraph.dev/v1" {
		return rgerrors.New(rgerrors.Graph, "apiVersion must be releasegraph.dev/v1")
	}
	if len(g.Projects) == 0 {
		return rgerrors.New(rgerrors.Graph, "graph has no projects")
	}
	ids := make([]string, 0, len(g.Projects))
	for id := range g.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		project := g.Projects[id]
		if project.ID == "" {
			project.ID = id
		}
		if project.ID != id {
			return rgerrors.New(rgerrors.Graph, fmt.Sprintf("project key %q does not match id %q", id, project.ID))
		}
		if project.Repo.Owner == "" || project.Repo.Name == "" {
			return rgerrors.New(rgerrors.Graph, fmt.Sprintf("project %q requires repo.owner and repo.name", id))
		}
		if project.Kind == "" {
			project.Kind = rgdomain.NodeKindRelease
		}
		switch project.Kind {
		case rgdomain.NodeKindRelease, rgdomain.NodeKindDeploy, rgdomain.NodeKindReconcileOnly:
		default:
			return rgerrors.New(rgerrors.Graph, fmt.Sprintf("project %q has invalid kind %q", id, project.Kind))
		}
		seen := map[string]bool{}
		for _, dep := range project.DependsOn {
			if dep.ID == id {
				return rgerrors.New(rgerrors.Graph, fmt.Sprintf("project %q depends on itself", id))
			}
			if _, ok := g.Projects[dep.ID]; !ok {
				return rgerrors.New(rgerrors.Graph, fmt.Sprintf("project %q depends on missing project %q", id, dep.ID))
			}
			condition := dep.Condition
			if condition == "" {
				condition = rgdomain.ConditionHealthy
			}
			switch condition {
			case rgdomain.ConditionHealthy, rgdomain.ConditionComplete:
			default:
				return rgerrors.New(rgerrors.Graph, fmt.Sprintf("project %q has invalid dependency condition %q", id, dep.Condition))
			}
			if seen[dep.ID] {
				return rgerrors.New(rgerrors.Graph, fmt.Sprintf("duplicate dependency %s -> %s", id, dep.ID))
			}
			seen[dep.ID] = true
		}
		g.Projects[id] = project
	}
	if cycle := findCycle(g); cycle != nil {
		return rgerrors.New(rgerrors.Graph, "cycle: "+strings.Join(cycle, " -> "))
	}
	return nil
}

func TopSort(g *rgdomain.ReleaseGraph) ([]string, error) {
	if err := Validate(g); err != nil {
		return nil, err
	}
	indegree := map[string]int{}
	ids := make([]string, 0, len(g.Projects))
	for id, project := range g.Projects {
		ids = append(ids, id)
		if _, ok := indegree[id]; !ok {
			indegree[id] = 0
		}
		for _, dep := range project.DependsOn {
			indegree[dep.ID] = indegree[dep.ID]
		}
	}
	for id, project := range g.Projects {
		indegree[id] = len(project.DependsOn)
	}
	sort.Strings(ids)
	queue := append([]string(nil), ids...)
	queue = queue[:0]
	for _, id := range ids {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	order := []string{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		downstream := Downstream(g, id)
		sort.Strings(downstream)
		for _, next := range downstream {
			indegree[next]--
			if indegree[next] == 0 {
				queue = insertSorted(queue, next)
			}
		}
	}
	if len(order) != len(g.Projects) {
		if cycle := findCycle(g); cycle != nil {
			return nil, rgerrors.New(rgerrors.Graph, "cycle: "+strings.Join(cycle, " -> "))
		}
		return nil, rgerrors.New(rgerrors.Graph, "invalid graph")
	}
	return order, nil
}

func BuildView(g *rgdomain.ReleaseGraph) (*View, error) {
	order, err := TopSort(g)
	if err != nil {
		return nil, err
	}
	view := &View{Order: order, Ready: []string{}, Blocked: map[string][]string{}, Upstream: map[string][]string{}, Downstream: map[string][]string{}}
	for _, id := range order {
		up := make([]string, 0, len(g.Projects[id].DependsOn))
		for _, dep := range g.Projects[id].DependsOn {
			up = append(up, dep.ID)
		}
		sort.Strings(up)
		view.Upstream[id] = up
		if len(up) == 0 {
			view.Ready = append(view.Ready, id)
		} else {
			view.Blocked[id] = up
		}
		for _, depID := range up {
			view.Downstream[depID] = append(view.Downstream[depID], id)
		}
	}
	return view, nil
}

func Downstream(g *rgdomain.ReleaseGraph, id string) []string {
	out := []string{}
	for other, project := range g.Projects {
		for _, dep := range project.DependsOn {
			if dep.ID == id {
				out = append(out, other)
			}
		}
	}
	sort.Strings(out)
	return out
}

func Mermaid(g *rgdomain.ReleaseGraph) (string, error) {
	if _, err := TopSort(g); err != nil {
		return "", err
	}
	ids := make([]string, 0, len(g.Projects))
	for id := range g.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	lines := []string{"graph TD"}
	for _, id := range ids {
		for _, dep := range g.Projects[id].DependsOn {
			lines = append(lines, fmt.Sprintf("  %s --> %s", sanitize(dep.ID), sanitize(id)))
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func insertSorted(values []string, value string) []string {
	i := sort.SearchStrings(values, value)
	values = append(values, "")
	copy(values[i+1:], values[i:])
	values[i] = value
	return values
}
func sanitize(id string) string { return strings.NewReplacer("-", "_", ".", "_", "/", "_").Replace(id) }

func findCycle(g *rgdomain.ReleaseGraph) []string {
	state := map[string]int{}
	stack := []string{}
	var visit func(string) []string
	visit = func(id string) []string {
		if state[id] == 1 {
			for i, v := range stack {
				if v == id {
					return append(append([]string{}, stack[i:]...), id)
				}
			}
		}
		if state[id] != 0 {
			return nil
		}
		state[id] = 1
		stack = append(stack, id)
		deps := make([]string, 0, len(g.Projects[id].DependsOn))
		for _, dep := range g.Projects[id].DependsOn {
			deps = append(deps, dep.ID)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			if cycle := visit(dep); cycle != nil {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return nil
	}
	ids := make([]string, 0, len(g.Projects))
	for id := range g.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if cycle := visit(id); cycle != nil {
			return cycle
		}
	}
	return nil
}
