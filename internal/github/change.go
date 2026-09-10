package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	rgerrors "github.com/redtidev1918/releasegraph/internal/errors"
)

// WorkflowRun is the subset of a workflow run used for canary evidence.
type WorkflowRun struct {
	ID         int64  `json:"id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Event      string `json:"event"`
	HeadBranch string `json:"head_branch"`
	HTMLURL    string `json:"html_url"`
	CreatedAt  string `json:"created_at"`
}

// RecentWorkflowRuns returns the most recent runs of one workflow file.
func (b *Bound) RecentWorkflowRuns(ctx context.Context, repo, workflowFile string, limit int) ([]WorkflowRun, error) {
	if err := b.guardTarget(repo); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	var runs struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	path := fmt.Sprintf("repos/%s/actions/workflows/%s/runs?per_page=%d", repo, workflowFile, limit)
	if _, err := b.client.GetOptional(ctx, path, &runs); err != nil {
		return nil, err
	}
	return runs.WorkflowRuns, nil
}

// LatestCompletedWorkflowRun returns the most recent run that has finished,
// skipping queued and in-progress runs.
func (b *Bound) LatestCompletedWorkflowRun(ctx context.Context, repo, workflowFile string) (WorkflowRun, bool, error) {
	runs, err := b.RecentWorkflowRuns(ctx, repo, workflowFile, 10)
	if err != nil {
		return WorkflowRun{}, false, err
	}
	for _, run := range runs {
		if run.Status == "completed" && run.Conclusion != "" {
			return run, true, nil
		}
	}
	return WorkflowRun{}, false, nil
}

// FileUpdate describes a reviewable single-file change on a new branch.
type FileUpdate struct {
	Repo      string
	Path      string
	Branch    string
	Message   string
	Title     string
	Body      string
	Transform func(string) (string, error)
}

// OpenChangePR commits a transformed file onto a fresh branch and opens a pull
// request. Rollout uses this so an infrastructure upgrade is reviewable,
// auditable and reversible, never a direct push to main.
func (b *Bound) OpenChangePR(ctx context.Context, update FileUpdate) (string, error) {
	if err := b.guardTarget(update.Repo); err != nil {
		return "", err
	}
	defaultBranch, err := b.client.DefaultBranch(ctx, update.Repo)
	if err != nil {
		return "", err
	}

	// Branch from the current default branch.
	var branch struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := b.client.Get(ctx, fmt.Sprintf("repos/%s/git/ref/heads/%s", update.Repo, defaultBranch), &branch); err != nil {
		return "", err
	}
	createBranch := map[string]string{"ref": "refs/heads/" + update.Branch, "sha": branch.Object.SHA}
	if _, status, err := b.client.request(ctx, http.MethodPost, fmt.Sprintf("repos/%s/git/refs", update.Repo), mustJSON(createBranch)); err != nil {
		return "", err
	} else if status == http.StatusUnprocessableEntity {
		// The branch already exists: an earlier rollout attempt is still open.
		return "", rgerrors.New(rgerrors.VersionConflict, fmt.Sprintf("branch %s already exists in %s (a rollout PR is probably still open)", update.Branch, update.Repo))
	}

	// Read, transform, commit.
	var file struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err := b.client.Get(ctx, fmt.Sprintf("repos/%s/contents/%s?ref=%s", update.Repo, update.Path, url.QueryEscape(defaultBranch)), &file); err != nil {
		return "", err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return "", rgerrors.Wrap(rgerrors.InvariantViolation, "decode workflow file", err)
	}
	next, err := update.Transform(string(decoded))
	if err != nil {
		return "", err
	}
	if next == string(decoded) {
		return "", rgerrors.New(rgerrors.VersionConflict, update.Repo+" already carries the requested change")
	}
	payload := map[string]string{
		"message": update.Message,
		"content": base64.StdEncoding.EncodeToString([]byte(next)),
		"sha":     file.SHA,
		"branch":  update.Branch,
	}
	if _, status, err := b.client.request(ctx, http.MethodPut, fmt.Sprintf("repos/%s/contents/%s", update.Repo, update.Path), mustJSON(payload)); err != nil {
		return "", err
	} else if status < 200 || status >= 300 {
		return "", rgerrors.New(rgerrors.Transient, fmt.Sprintf("commit %s: status %d", update.Repo, status))
	}

	pr := map[string]any{"title": update.Title, "body": update.Body, "head": update.Branch, "base": defaultBranch}
	body, status, err := b.client.request(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls", update.Repo), mustJSON(pr))
	if err != nil {
		return "", err
	}
	if status == http.StatusUnprocessableEntity {
		return "", rgerrors.New(rgerrors.VersionConflict, fmt.Sprintf("a pull request for %s may already exist: %s", update.Branch, strings.TrimSpace(string(body))))
	}
	if status < 200 || status >= 300 {
		return "", rgerrors.New(rgerrors.Transient, fmt.Sprintf("open PR in %s: status %d %s", update.Repo, status, strings.TrimSpace(string(body))))
	}
	var created struct {
		HTMLURL string `json:"html_url"`
	}
	_ = decodeJSON(body, &created)
	return created.HTMLURL, nil
}
