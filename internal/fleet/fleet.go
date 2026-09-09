package fleet

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strings"

	rgdomain "github.com/redtidev1918/release-infra/internal/domain"
	"github.com/redtidev1918/release-infra/internal/github"
)

type Repository struct {
	Name           string          `json:"name"`
	Visibility     string          `json:"visibility"`
	DefaultBranch  string          `json:"defaultBranch"`
	Fork           bool            `json:"fork"`
	Archived       bool            `json:"archived"`
	Managed        bool            `json:"managed"`
	Classification string          `json:"classification"`
	Health         rgdomain.Health `json:"health"`
	Signals        []string        `json:"signals"`
}

type Fleet struct {
	Repositories []Repository `json:"repositories"`
}

func Discover(ctx context.Context, client *github.Client, owner string, publicOnly bool) (*Fleet, error) {
	path := "user/repos?affiliation=owner&type=all"
	if publicOnly {
		path = fmt.Sprintf("users/%s/repos?type=all", url.PathEscape(owner))
	}
	raw, err := client.Paginate(ctx, path)
	if err != nil {
		return nil, err
	}
	out := &Fleet{Repositories: []Repository{}}
	for _, source := range raw {
		fullName, _ := source["full_name"].(string)
		repoOwner, _, _ := strings.Cut(fullName, "/")
		if !strings.EqualFold(repoOwner, owner) {
			continue
		}
		repo := Repository{
			Name:           fullName,
			Visibility:     stringValue(source, "visibility", "private"),
			DefaultBranch:  stringValue(source, "default_branch", ""),
			Fork:           boolValue(source, "fork"),
			Archived:       boolValue(source, "archived"),
			Signals:        []string{},
			Classification: "observe-only",
			Health:         rgdomain.HealthUnmanaged,
		}
		if publicOnly && repo.Visibility != "public" {
			continue
		}
		if repo.Archived {
			repo.Classification = "archived"
			repo.Health = rgdomain.HealthNoRelease
		} else if repo.Fork {
			repo.Classification = "fork"
			repo.Health = rgdomain.HealthNoRelease
		} else if policy, err := readPolicy(ctx, client, fullName, repo.DefaultBranch); err == nil && policy != "" {
			repo.Managed = true
			repo.Classification = "managed"
			repo.Health = rgdomain.HealthNeedsReview
			repo.Signals = append(repo.Signals, ".release-policy.yml")
		}
		sort.Strings(repo.Signals)
		out.Repositories = append(out.Repositories, repo)
	}
	sort.Slice(out.Repositories, func(i, j int) bool {
		return strings.ToLower(out.Repositories[i].Name) < strings.ToLower(out.Repositories[j].Name)
	})
	return out, nil
}

func readPolicy(ctx context.Context, client *github.Client, repo, branch string) (string, error) {
	if branch == "" {
		return "", nil
	}
	var file struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	path := fmt.Sprintf("repos/%s/contents/.release-policy.yml?ref=%s", repo, url.PathEscape(branch))
	if err := client.Get(ctx, path, &file); err != nil {
		return "", err
	}
	if file.Encoding != "base64" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func boolValue(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}
