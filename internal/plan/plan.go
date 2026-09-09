package plan

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/redtidev1918/releasegraph/internal/domain"
	"github.com/redtidev1918/releasegraph/internal/graph"
)

type View struct {
	Ready   []string            `json:"ready"`
	Blocked map[string][]string `json:"blocked"`
	Noop    []string            `json:"noop"`
}

func Evaluate(g *domain.ReleaseGraph, health map[string]domain.Health) (*View, error) {
	if _, err := graph.TopSort(g); err != nil {
		return nil, err
	}
	view := &View{Ready: []string{}, Blocked: map[string][]string{}, Noop: []string{}}
	ids := make([]string, 0, len(g.Projects))
	for id := range g.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if health[id] == domain.HealthHealthy {
			view.Noop = append(view.Noop, id)
			continue
		}
		blocked := []string{}
		for _, dep := range g.Projects[id].DependsOn {
			if !conditionSatisfied(dep.Condition, health[dep.ID]) {
				blocked = append(blocked, dep.ID)
			}
		}
		if len(blocked) == 0 {
			view.Ready = append(view.Ready, id)
		} else {
			sort.Strings(blocked)
			view.Blocked[id] = blocked
		}
	}
	return view, nil
}

func LoadHealth(path string) (map[string]domain.Health, error) {
	if path == "" {
		return map[string]domain.Health{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	health := map[string]domain.Health{}
	for id, value := range values {
		health[id] = domain.Health(value)
	}
	return health, nil
}

func conditionSatisfied(condition domain.DependencyCondition, actual domain.Health) bool {
	if condition == "" {
		condition = domain.ConditionHealthy
	}
	if condition == domain.ConditionHealthy {
		return actual == domain.HealthHealthy
	}
	return actual == domain.HealthHealthy || actual == domain.HealthNoop
}
