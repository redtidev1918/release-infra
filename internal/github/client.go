package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	rgerrors "github.com/redtidev1918/release-infra/internal/errors"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New() *Client {
	base := os.Getenv("GITHUB_API_URL")
	if base == "" {
		base = "https://api.github.com"
	}
	return &Client{baseURL: strings.TrimRight(base, "/"), token: os.Getenv("GITHUB_TOKEN"), http: &http.Client{Timeout: 30 * time.Second}}
}

func NewForTest(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) Get(ctx context.Context, path string, target any) error {
	body, _, err := c.get(ctx, path)
	if err != nil {
		return err
	}
	if target != nil {
		return json.Unmarshal(body, target)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, http.Header, error) {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		endpoint := c.baseURL
		if !strings.HasSuffix(endpoint, "/") {
			endpoint += "/"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+strings.TrimPrefix(path, "/"), nil)
		if err != nil {
			return nil, nil, rgerrors.Wrap(rgerrors.Transient, "build GitHub request", err)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := c.http.Do(req)
		if err != nil {
			last = rgerrors.Wrap(rgerrors.Transient, "GitHub request", err)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			last = rgerrors.Wrap(rgerrors.Transient, "read GitHub response", readErr)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, resp.Header, nil
		}
		if resp.StatusCode == 401 {
			return nil, nil, rgerrors.New(rgerrors.Authentication, "GitHub authentication failed")
		}
		if resp.StatusCode == 403 {
			return nil, nil, rgerrors.New(rgerrors.Permission, "GitHub permission denied")
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			last = rgerrors.New(rgerrors.Transient, fmt.Sprintf("GitHub %s", resp.Status))
			continue
		}
		return nil, nil, rgerrors.New(rgerrors.Transient, fmt.Sprintf("GitHub %s: %s", resp.Status, strings.TrimSpace(string(body))))
	}
	return nil, nil, last
}

func (c *Client) Paginate(ctx context.Context, path string) ([]map[string]any, error) {
	values := []map[string]any{}
	next := path
	if !strings.Contains(next, "per_page=") {
		separator := "?"
		if strings.Contains(next, "?") {
			separator = "&"
		}
		next += separator + "per_page=100"
	}
	for next != "" {
		var page []map[string]any
		body, headers, err := c.get(ctx, next)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		values = append(values, page...)
		next = nextLink(headers.Get("Link"), c.baseURL)
	}
	return values, nil
}

func nextLink(header, baseURL string) string {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if len(fields) != 2 || !strings.Contains(fields[1], `rel="next"`) {
			continue
		}
		raw := strings.TrimSpace(fields[0])
		raw = strings.TrimPrefix(raw, "<")
		raw = strings.TrimSuffix(raw, ">")
		if _, err := url.Parse(raw); err != nil {
			return ""
		}
		base, err := url.Parse(baseURL)
		if err != nil {
			return ""
		}
		return strings.TrimPrefix(raw, base.String())
	}
	return ""
}
