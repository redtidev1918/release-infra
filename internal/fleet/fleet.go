package fleet

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	rgdomain "github.com/redtidev1918/releasegraph/internal/domain"
	"github.com/redtidev1918/releasegraph/internal/github"
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
	LatestRelease  string          `json:"latestRelease,omitempty"`
	DraftCount     int             `json:"draftCount"`
	AssetCount     int             `json:"assetCount"`
	Signals        []string        `json:"signals"`
}

type Fleet struct {
	Repositories []Repository `json:"repositories"`
}

func Discover(ctx context.Context, client *github.Client, owner string, publicOnly bool) (*Fleet, error) {
	// GitHub rejects combining affiliation with type (422); affiliation=owner
	// already spans every repo type and visibility.
	path := "user/repos?affiliation=owner"
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
		} else if policy, found, err := client.ReadFile(ctx, fullName, ".release-policy.yml", repo.DefaultBranch); err != nil {
			return nil, err
		} else if found && len(policy) > 0 {
			repo.Managed = true
			repo.Classification = "managed"
			repo.Health = rgdomain.HealthNeedsReview
			repo.Signals = append(repo.Signals, ".release-policy.yml")
			if err := enrichReleaseHealth(ctx, client, &repo); err != nil {
				return nil, err
			}
		}
		sort.Strings(repo.Signals)
		out.Repositories = append(out.Repositories, repo)
	}
	sort.Slice(out.Repositories, func(i, j int) bool {
		return strings.ToLower(out.Repositories[i].Name) < strings.ToLower(out.Repositories[j].Name)
	})
	return out, nil
}

func enrichReleaseHealth(ctx context.Context, client *github.Client, repo *Repository) error {
	releases, err := client.Releases(ctx, repo.Name)
	if err != nil {
		return err
	}
	var latest map[string]any
	for _, release := range releases {
		if release["draft"] == true || release["prerelease"] == true {
			continue
		}
		latest = release
		break
	}
	repo.DraftCount = 0
	for _, release := range releases {
		if release["draft"] == true {
			repo.DraftCount++
		}
	}
	if latest != nil {
		repo.LatestRelease, _ = latest["tag_name"].(string)
		if assets, ok := latest["assets"].([]any); ok {
			repo.AssetCount = len(assets)
		}
	}
	return nil
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
