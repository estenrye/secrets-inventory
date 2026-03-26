package githubclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
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
