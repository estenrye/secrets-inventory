package githubclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/google/go-github/v84/github"
	"golang.org/x/oauth2"

	"secret-inventory/internal/config"
)

var ErrNotModified = errors.New("not modified")

type Client struct {
	gh   *github.Client
	etag *ETagStore
	fc   *FileCache
}

type listEnvironmentsResponse struct {
	Environments []*github.Environment `json:"environments"`
}

type EnvironmentInfo struct {
	Name string
	ID   int64
}

type listEnvironmentSecretsResponse struct {
	Secrets []*github.Secret `json:"secrets"`
}

type listEnvironmentVariablesResponse struct {
	Variables []*github.ActionsVariable `json:"variables"`
}

type FileMeta struct {
	CacheKey string
	ETag     string
}

func New(token, baseURL string, etag *ETagStore, fileCacheDir string) *Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	hc := oauth2.NewClient(context.Background(), ts)
	gh := github.NewClient(hc)
	if baseURL != "" {
		// For GitHub Enterprise Server, baseURL should typically be like:
		//   https://github.example.com/api/v3/
		ent, err := gh.WithEnterpriseURLs(baseURL, baseURL)
		if err == nil {
			gh = ent
		}
	}
	if etag == nil {
		etag = NewETagStore()
	}
	fc := (*FileCache)(nil)
	if fileCacheDir != "" {
		fc = NewFileCache(fileCacheDir)
	}
	return &Client{gh: gh, etag: etag, fc: fc}
}

func (c *Client) SaveETags(path string) error {
	return c.etag.Save(path)
}

func (c *Client) StoreETag(meta FileMeta) {
	if meta.CacheKey == "" || meta.ETag == "" {
		return
	}
	c.etag.Set(meta.CacheKey, meta.ETag)
}

func (c *Client) ResolveTargets(ctx context.Context, t config.Targets) ([]*github.Repository, error) {
	seen := map[string]bool{}
	var out []*github.Repository

	for _, full := range t.Repos {
		owner, name, ok := strings.Cut(full, "/")
		if !ok {
			return nil, fmt.Errorf("invalid repo in targets.repos: %q (expected owner/name)", full)
		}
		r, _, err := c.gh.Repositories.Get(ctx, owner, name)
		if err != nil {
			return nil, err
		}
		key := r.GetOwner().GetLogin() + "/" + r.GetName()
		if !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}

	for _, org := range t.Orgs {
		opt := &github.RepositoryListByOrgOptions{Type: "all", ListOptions: github.ListOptions{PerPage: 100}}
		for {
			repos, resp, err := c.gh.Repositories.ListByOrg(ctx, org, opt)
			if err != nil {
				return nil, err
			}
			for _, r := range repos {
				key := r.GetOwner().GetLogin() + "/" + r.GetName()
				if !seen[key] {
					seen[key] = true
					out = append(out, r)
				}
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
	}

	for _, user := range t.Users {
		opt := &github.RepositoryListByUserOptions{Type: "all", ListOptions: github.ListOptions{PerPage: 100}}
		for {
			repos, resp, err := c.gh.Repositories.ListByUser(ctx, user, opt)
			if err != nil {
				return nil, err
			}
			for _, r := range repos {
				key := r.GetOwner().GetLogin() + "/" + r.GetName()
				if !seen[key] {
					seen[key] = true
					out = append(out, r)
				}
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
	}

	return out, nil
}

func (c *Client) DefaultBranchSHA(ctx context.Context, repo *github.Repository) (string, error) {
	owner := repo.GetOwner().GetLogin()
	name := repo.GetName()
	branch := repo.GetDefaultBranch()
	if owner == "" || name == "" || branch == "" {
		return "", fmt.Errorf("missing owner/name/default_branch for repo")
	}
	b, _, err := c.gh.Repositories.GetBranch(ctx, owner, name, branch, 0)
	if err != nil {
		return "", err
	}
	if b == nil || b.Commit == nil {
		return "", fmt.Errorf("no commit info for %s/%s branch %s", owner, name, branch)
	}
	sha := b.Commit.GetSHA()
	if sha == "" {
		return "", fmt.Errorf("empty sha for %s/%s branch %s", owner, name, branch)
	}
	return sha, nil
}

func (c *Client) ListWorkflowFiles(ctx context.Context, repo *github.Repository) ([]string, error) {
	owner := repo.GetOwner().GetLogin()
	name := repo.GetName()

	_, dir, _, err := c.gh.Repositories.GetContents(ctx, owner, name, ".github/workflows", &github.RepositoryContentGetOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range dir {
		if f == nil {
			continue
		}
		p := f.GetPath()
		if strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml") {
			out = append(out, p)
		}
	}
	return out, nil
}

func (c *Client) GetFile(ctx context.Context, repo *github.Repository, filePath string) (string, FileMeta, error) {
	owner := repo.GetOwner().GetLogin()
	name := repo.GetName()

	reqPath := path.Join("repos", owner, name, "contents", filePath)
	req, err := c.gh.NewRequest("GET", reqPath, nil)
	if err != nil {
		return "", FileMeta{}, err
	}
	cacheKey := "GET " + req.URL.String()
	if et, ok := c.etag.Get(cacheKey); ok {
		req.Header.Set("If-None-Match", et)
	}

	var content github.RepositoryContent
	resp, err := c.gh.Do(ctx, req, &content)
	if err != nil {
		// go-github uses ErrorResponse for non-2xx
		var er *github.ErrorResponse
		if errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == http.StatusNotModified {
			if c.fc != nil {
				if cached, ok := c.fc.Get(cacheKey); ok {
					return cached, FileMeta{CacheKey: cacheKey, ETag: ""}, nil
				}
			}
			return "", FileMeta{CacheKey: cacheKey, ETag: ""}, ErrNotModified
		}
		return "", FileMeta{}, err
	}

	etag := ""
	if resp != nil {
		etag = resp.Header.Get("ETag")
	}

	text, err := content.GetContent()
	if err != nil {
		return "", FileMeta{}, err
	}
	if c.fc != nil {
		_ = c.fc.Set(cacheKey, text)
	}
	return text, FileMeta{CacheKey: cacheKey, ETag: etag}, nil
}

func (c *Client) ListRepoSecretNames(ctx context.Context, owner, repo string) ([]string, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []string
	for {
		secrets, resp, err := c.gh.Actions.ListRepoSecrets(ctx, owner, repo, opt)
		if err != nil {
			return nil, err
		}
		if secrets != nil {
			for _, s := range secrets.Secrets {
				if s == nil {
					continue
				}
				name := strings.TrimSpace(s.Name)
				if name != "" {
					out = append(out, name)
				}
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	sort.Strings(out)
	return out, nil
}

func (c *Client) ListRepoVariableNames(ctx context.Context, owner, repo string) ([]string, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []string
	for {
		vars, resp, err := c.gh.Actions.ListRepoVariables(ctx, owner, repo, opt)
		if err != nil {
			return nil, err
		}
		if vars != nil {
			for _, v := range vars.Variables {
				if v == nil {
					continue
				}
				name := strings.TrimSpace(v.Name)
				if name != "" {
					out = append(out, name)
				}
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	sort.Strings(out)
	return out, nil
}

func (c *Client) ListOrgSecretNames(ctx context.Context, org string) ([]string, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []string
	for {
		secrets, resp, err := c.gh.Actions.ListOrgSecrets(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		if secrets != nil {
			for _, s := range secrets.Secrets {
				if s == nil {
					continue
				}
				name := strings.TrimSpace(s.Name)
				if name != "" {
					out = append(out, name)
				}
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	sort.Strings(out)
	return out, nil
}

func (c *Client) ListOrgVariableNames(ctx context.Context, org string) ([]string, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []string
	for {
		vars, resp, err := c.gh.Actions.ListOrgVariables(ctx, org, opt)
		if err != nil {
			return nil, err
		}
		if vars != nil {
			for _, v := range vars.Variables {
				if v == nil {
					continue
				}
				name := strings.TrimSpace(v.Name)
				if name != "" {
					out = append(out, name)
				}
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	sort.Strings(out)
	return out, nil
}

func (c *Client) ListEnvironments(ctx context.Context, owner, repo string) ([]EnvironmentInfo, error) {
	// Not currently exposed as a go-github convenience method in v84; use raw REST.
	reqPath := path.Join("repos", owner, repo, "environments")
	req, err := c.gh.NewRequest("GET", reqPath, nil)
	if err != nil {
		return nil, err
	}
	var respBody listEnvironmentsResponse
	_, err = c.gh.Do(ctx, req, &respBody)
	if err != nil {
		return nil, err
	}
	out := make([]EnvironmentInfo, 0)
	for _, e := range respBody.Environments {
		if e == nil {
			continue
		}
		name := strings.TrimSpace(e.GetName())
		if name == "" {
			continue
		}
		out = append(out, EnvironmentInfo{Name: name, ID: e.GetID()})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (c *Client) ListEnvironmentSecretNames(ctx context.Context, owner, repo, environment string) ([]string, error) {
	reqPath := path.Join("repos", owner, repo, "environments", environment, "secrets")
	req, err := c.gh.NewRequest("GET", reqPath, nil)
	if err != nil {
		return nil, err
	}
	var respBody listEnvironmentSecretsResponse
	_, err = c.gh.Do(ctx, req, &respBody)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range respBody.Secrets {
		if s == nil {
			continue
		}
		name := strings.TrimSpace(s.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (c *Client) ListEnvironmentVariableNames(ctx context.Context, owner, repo, environment string) ([]string, error) {
	reqPath := path.Join("repos", owner, repo, "environments", environment, "variables")
	req, err := c.gh.NewRequest("GET", reqPath, nil)
	if err != nil {
		return nil, err
	}
	var respBody listEnvironmentVariablesResponse
	_, err = c.gh.Do(ctx, req, &respBody)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range respBody.Variables {
		if v == nil {
			continue
		}
		name := strings.TrimSpace(v.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}
