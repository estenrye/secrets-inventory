package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v84/github"
	"gopkg.in/yaml.v3"

	"secret-inventory/internal/analyze"
	"secret-inventory/internal/config"
	"secret-inventory/internal/githubclient"
	"secret-inventory/internal/model"
	"secret-inventory/internal/report"
)

func isPlaceholderPath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return false
	}
	return strings.Contains(p, "__THIS_REPO__") || strings.Contains(p, "__BUILDER_CHECKOUT_DIR__")
}

func sourceKeyForFinding(f model.Finding, repoRef map[string]string) string {
	repoKey := f.RepoOwner + "/" + f.RepoName
	ref := strings.TrimSpace(repoRef[repoKey])
	if ref == "" {
		return ""
	}
	if f.LineStart <= 0 {
		return ""
	}
	path := ""
	switch f.FileKind {
	case "workflow_yaml":
		path = strings.TrimSpace(f.WorkflowPath)
	default:
		path = strings.TrimSpace(f.FilePath)
	}
	if path == "" || isPlaceholderPath(path) {
		return ""
	}
	return fmt.Sprintf("%s@%s:%s#L%d", repoKey, ref, path, f.LineStart)
}

func fallbackKeyForFinding(f model.Finding) string {
	// Must be stable across runs and include enough fields to avoid accidental collisions.
	return strings.Join([]string{
		f.RepoOwner,
		f.RepoName,
		f.WorkflowPath,
		f.JobID,
		fmt.Sprintf("%d", f.StepIndex),
		f.StepName,
		f.FieldPath,
		f.FileKind,
		f.FilePath,
		f.ContextKind,
		f.ActionUses,
		fmt.Sprintf("%d", f.LineStart),
		fmt.Sprintf("%d", f.ColStart),
	}, "|")
}

func mergeFindings(snap *model.Snapshot) []model.MergedFinding {
	repoRef := map[string]string{}
	for _, r := range snap.Repos {
		key := r.Owner + "/" + r.Name
		ref := strings.TrimSpace(r.ScannedRef)
		if ref == "" {
			ref = strings.TrimSpace(r.DefaultBranch)
		}
		repoRef[key] = ref
	}

	type group struct {
		sourceKey string
		items     []model.Finding
	}
	groups := map[string]*group{}

	for i := range snap.Findings {
		f := snap.Findings[i]
		sk := sourceKeyForFinding(f, repoRef)
		if sk != "" {
			f.SourceKey = sk
			snap.Findings[i] = f
		}

		gk := ""
		if sk != "" {
			gk = sk
		} else {
			gk = fallbackKeyForFinding(f)
		}
		gk = gk + "|" + f.RefType + "." + f.RefName

		g := groups[gk]
		if g == nil {
			g = &group{sourceKey: sk, items: []model.Finding{}}
			groups[gk] = g
		}
		g.items = append(g.items, f)
	}

	merged := make([]model.MergedFinding, 0, len(groups))
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		rep := g.items[0]
		for i := 1; i < len(g.items); i++ {
			it := g.items[i]
			if rep.Expression == "" {
				rep.Expression = it.Expression
			}
			if rep.FilePath == "" {
				rep.FilePath = it.FilePath
			}
			if rep.WorkflowPath == "" {
				rep.WorkflowPath = it.WorkflowPath
			}
			if rep.FileKind == "" {
				rep.FileKind = it.FileKind
			}
			if rep.LineStart == 0 {
				rep.LineStart = it.LineStart
			}
			if rep.LineEnd == 0 {
				rep.LineEnd = it.LineEnd
			}
			if rep.ColStart == 0 {
				rep.ColStart = it.ColStart
			}
			if rep.ColEnd == 0 {
				rep.ColEnd = it.ColEnd
			}
			if rep.Origin == "" {
				rep.Origin = it.Origin
			}
			if rep.ContextKind == "" {
				rep.ContextKind = it.ContextKind
			}
			if rep.ActionUses == "" {
				rep.ActionUses = it.ActionUses
			}
		}

		wf := strings.TrimSpace(rep.WorkflowPath)
		for i := 1; i < len(g.items); i++ {
			if strings.TrimSpace(g.items[i].WorkflowPath) != wf {
				wf = ""
				break
			}
		}

		ctxs := make([]model.FindingContext, 0, len(g.items))
		for _, it := range g.items {
			ctxs = append(ctxs, model.FindingContext{
				WorkflowPath: it.WorkflowPath,
				Environment:  it.Environment,
				JobID:        it.JobID,
				StepIndex:    it.StepIndex,
				StepName:     it.StepName,
				FieldPath:    it.FieldPath,
				ContextKind:  it.ContextKind,
				ActionUses:   it.ActionUses,
				Origin:       it.Origin,
			})
		}
		sort.SliceStable(ctxs, func(i, j int) bool {
			a := ctxs[i]
			b := ctxs[j]
			if a.WorkflowPath != b.WorkflowPath {
				return a.WorkflowPath < b.WorkflowPath
			}
			if a.JobID != b.JobID {
				return a.JobID < b.JobID
			}
			if a.StepIndex != b.StepIndex {
				return a.StepIndex < b.StepIndex
			}
			if a.StepName != b.StepName {
				return a.StepName < b.StepName
			}
			return a.FieldPath < b.FieldPath
		})

		merged = append(merged, model.MergedFinding{
			RepoOwner:    rep.RepoOwner,
			RepoName:     rep.RepoName,
			RefType:      rep.RefType,
			RefName:      rep.RefName,
			Expression:   rep.Expression,
			FilePath:     rep.FilePath,
			FileKind:     rep.FileKind,
			WorkflowPath: wf,
			LineStart:    rep.LineStart,
			LineEnd:      rep.LineEnd,
			ColStart:     rep.ColStart,
			ColEnd:       rep.ColEnd,
			SourceKey:    g.sourceKey,
			Count:        len(g.items),
			Contexts:     ctxs,
		})
	}

	sort.SliceStable(merged, func(i, j int) bool {
		a := merged[i]
		b := merged[j]
		ar := a.RepoOwner + "/" + a.RepoName
		br := b.RepoOwner + "/" + b.RepoName
		if ar != br {
			return ar < br
		}
		if a.FileKind != b.FileKind {
			return a.FileKind < b.FileKind
		}
		if a.FilePath != b.FilePath {
			return a.FilePath < b.FilePath
		}
		if a.LineStart != b.LineStart {
			return a.LineStart < b.LineStart
		}
		if a.RefType != b.RefType {
			return a.RefType < b.RefType
		}
		if a.RefName != b.RefName {
			return a.RefName < b.RefName
		}
		return a.SourceKey < b.SourceKey
	})

	return merged
}

type scanArgs struct {
	configPath  string
	outDir      string
	deepInspect bool
	verbose     bool
}

func httpStatusFromErr(err error) (int, bool) {
	var er *github.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		return er.Response.StatusCode, true
	}
	return 0, false
}

func repoKey(owner, repo string) string {
	return owner + "/" + repo
}

func scanWarningMessage(kind string) string {
	switch kind {
	case "workflow_read_forbidden":
		return "Token lacks permission to read GitHub Actions workflow files for these repositories (requires Contents: Read)."
	case "script_read_forbidden", "local_action_read_forbidden":
		return "Token lacks permission to read referenced scripts/local actions for these repositories (requires Contents: Read)."
	default:
		return "Token lacks permission to read repository contents for these repositories (requires Contents: Read)."
	}
}

func scanWarningConsoleLine(kind, owner, repo string) string {
	switch kind {
	case "workflow_read_forbidden":
		return fmt.Sprintf("repo %s cannot be scanned: GitHub returned 403 when reading workflow files (requires Contents: Read)", repoKey(owner, repo))
	case "script_read_forbidden", "local_action_read_forbidden":
		return fmt.Sprintf("repo %s scanned partially: GitHub returned 403 when reading referenced scripts/local actions (requires Contents: Read)", repoKey(owner, repo))
	default:
		return fmt.Sprintf("repo %s scanned partially: GitHub returned 403 when reading repository contents (requires Contents: Read)", repoKey(owner, repo))
	}
}

func githubWebBase(apiBase string) string {
	if apiBase == "" {
		return "https://github.com"
	}
	// Typical GHES API base: https://github.example.com/api/v3/
	// Web base:             https://github.example.com/
	s := strings.TrimSpace(apiBase)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, "/api/v3")
	if s == "" {
		return "https://github.com"
	}
	return s
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(argv []string, stdout, stderr io.Writer) error {
	if len(argv) == 0 {
		return usageError{}
	}

	switch argv[0] {
	case "scan":
		args, err := parseScanArgs(argv[1:])
		if err != nil {
			return err
		}
		return runScan(args, stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, usage())
		return nil
	default:
		return usageError{}
	}
}

type usageError struct{}

func (usageError) Error() string {
	return usage()
}

func usage() string {
	return "Usage:\n  secret-inventory scan --config <config.yml> --out <out-dir> [--deep-inspect] [--verbose]\n"
}

func parseScanArgs(argv []string) (scanArgs, error) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var a scanArgs
	fs.StringVar(&a.configPath, "config", "", "path to config yaml")
	fs.StringVar(&a.outDir, "out", "out", "output directory")
	fs.BoolVar(&a.deepInspect, "deep-inspect", false, "list declared secrets/variables across org/repo/environment scopes and report unused")
	fs.BoolVar(&a.verbose, "verbose", false, "emit verbose warnings as they occur")
	if err := fs.Parse(argv); err != nil {
		return scanArgs{}, err
	}
	if a.configPath == "" {
		return scanArgs{}, errors.New("missing --config")
	}
	return a, nil
}

func runScan(args scanArgs, stdout, stderr io.Writer) error {
	cfgBytes, err := os.ReadFile(args.configPath)
	if err != nil {
		return err
	}
	var cfg config.Config
	if err := yaml.Unmarshal(cfgBytes, &cfg); err != nil {
		return err
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}

	token := cfg.GitHub.Token
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return errors.New("missing GitHub token: set config.github.token or GITHUB_TOKEN")
	}

	if err := os.MkdirAll(args.outDir, 0o755); err != nil {
		return err
	}
	cacheDir := filepath.Join(args.outDir, ".cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}

	etagPath := filepath.Join(cacheDir, "etags.json")
	etagStore, _ := githubclient.LoadETagStore(etagPath)
	fileCacheDir := filepath.Join(cacheDir, "files")

	ctx := context.Background()
	gh := githubclient.New(token, cfg.GitHub.BaseURL, etagStore, fileCacheDir)

	repos, err := gh.ResolveTargets(ctx, cfg.Targets)
	if err != nil {
		return err
	}

	snapshot := model.Snapshot{
		GeneratedAt:   time.Now().UTC(),
		GitHubWebBase: githubWebBase(cfg.GitHub.BaseURL),
		Targets:       cfg.Targets,
		Repos:         make([]model.Repo, 0, len(repos)),
		Findings:      []model.Finding{},
	}

	warnKinds := map[string]struct{}{
		"workflow_read_forbidden":     {},
		"script_read_forbidden":       {},
		"local_action_read_forbidden": {},
	}
	_ = warnKinds
	warnByRepo := map[string]map[string]model.ScanWarning{}
	addWarn := func(kind, owner, repo, operation, p string, status int) {
		k := repoKey(owner, repo)
		m := warnByRepo[k]
		if m == nil {
			m = map[string]model.ScanWarning{}
			warnByRepo[k] = m
		}
		if _, ok := m[kind]; ok {
			return
		}
		w := model.ScanWarning{
			Kind:       kind,
			RepoOwner:  owner,
			RepoName:   repo,
			HTTPStatus: status,
			Operation:  operation,
			Path:       p,
			Message:    scanWarningMessage(kind),
		}
		m[kind] = w
		if args.verbose {
			fmt.Fprintf(stderr, "warning: %s\n", scanWarningConsoleLine(kind, owner, repo))
		}
	}

	scanner := analyze.NewScanner(analyze.ScannerOptions{
		ScriptExtensions: cfg.Scanner.ScriptExtensions,
		MaxFileBytes:     cfg.Scanner.MaxFileBytes,
		IncludeUnknown:   cfg.Scanner.IncludeUnknownEnv,
	})

	for _, r := range repos {
		owner := r.GetOwner().GetLogin()
		repoName := r.GetName()
		sha, shaErr := gh.DefaultBranchSHA(ctx, r)
		if shaErr != nil {
			fmt.Fprintf(os.Stderr, "warning: %s/%s: unable to resolve default branch SHA: %v\n", owner, repoName, shaErr)
		}
		snapshot.Repos = append(snapshot.Repos, model.Repo{
			Owner:         owner,
			Name:          repoName,
			DefaultBranch: r.GetDefaultBranch(),
			ScannedRef:    sha,
			Archived:      r.GetArchived(),
			Private:       r.GetPrivate(),
		})

		workflowFiles, err := gh.ListWorkflowFiles(ctx, r)
		if err != nil {
			if st, ok := httpStatusFromErr(err); ok {
				switch st {
				case http.StatusNotFound:
					continue
				case http.StatusForbidden:
					addWarn("workflow_read_forbidden", owner, repoName, "contents.list_workflows", ".github/workflows", st)
					continue
				}
			}
			fmt.Fprintf(stderr, "warning: %s/%s: %v\n", owner, repoName, err)
			continue
		}

		for _, wf := range workflowFiles {
			wfContent, meta, err := gh.GetFile(ctx, r, wf)
			if err != nil {
				if errors.Is(err, githubclient.ErrNotModified) {
					continue
				}
				if st, ok := httpStatusFromErr(err); ok && st == http.StatusForbidden {
					addWarn("workflow_read_forbidden", owner, repoName, "contents.get_file", wf, st)
					continue
				}
				fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", owner, repoName, wf, err)
				continue
			}

			wfFindings, additionalFiles, err := scanner.ScanWorkflowYAML(owner, repoName, wf, wfContent)
			if err != nil {
				fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", owner, repoName, wf, err)
			}
			snapshot.Findings = append(snapshot.Findings, wfFindings...)

			for _, f := range additionalFiles {
				content, _, err := gh.GetFile(ctx, r, f.Path)
				if err != nil {
					if errors.Is(err, githubclient.ErrNotModified) {
						continue
					}
					if st, ok := httpStatusFromErr(err); ok && st == http.StatusForbidden {
						kind := "script_read_forbidden"
						if strings.TrimSpace(f.Kind) != "script" {
							kind = "local_action_read_forbidden"
						}
						addWarn(kind, owner, repoName, "contents.get_file", f.Path, st)
						continue
					}
					fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", owner, repoName, f.Path, err)
					continue
				}
				fileFindings, moreFiles, err := scanner.ScanRepoFile(f, content)
				if err != nil {
					fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", owner, repoName, f.Path, err)
				}
				snapshot.Findings = append(snapshot.Findings, fileFindings...)

				for _, mf := range moreFiles {
					c2, _, err := gh.GetFile(ctx, r, mf.Path)
					if err != nil {
						if errors.Is(err, githubclient.ErrNotModified) {
							continue
						}
						if st, ok := httpStatusFromErr(err); ok && st == http.StatusForbidden {
							kind := "script_read_forbidden"
							if strings.TrimSpace(mf.Kind) != "script" {
								kind = "local_action_read_forbidden"
							}
							addWarn(kind, owner, repoName, "contents.get_file", mf.Path, st)
							continue
						}
						fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", owner, repoName, mf.Path, err)
						continue
					}
					ff2, _, err := scanner.ScanRepoFile(mf, c2)
					if err != nil {
						fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", owner, repoName, mf.Path, err)
					}
					snapshot.Findings = append(snapshot.Findings, ff2...)
				}
			}

			gh.StoreETag(meta)
		}
	}

	if len(warnByRepo) > 0 {
		byMessage := map[string]map[string]struct{}{}
		order := make([]string, 0)
		for _, byKind := range warnByRepo {
			for _, w := range byKind {
				snapshot.ScanWarnings = append(snapshot.ScanWarnings, w)
				msg := strings.TrimSpace(w.Message)
				repo := repoKey(w.RepoOwner, w.RepoName)
				set := byMessage[msg]
				if set == nil {
					set = map[string]struct{}{}
					byMessage[msg] = set
					order = append(order, msg)
				}
				set[repo] = struct{}{}
			}
		}
		for _, msg := range order {
			set := byMessage[msg]
			if len(set) == 0 {
				continue
			}
			repos := make([]string, 0, len(set))
			for r := range set {
				repos = append(repos, r)
			}
			sort.Strings(repos)
			fmt.Fprintf(stderr, "warning: %s for %s\n", msg, strings.Join(repos, ", "))
		}
	}

	if err := gh.SaveETags(etagPath); err != nil {
		fmt.Fprintf(stderr, "warning: failed to save etag cache: %v\n", err)
	}

	snapshot.MergedFindings = mergeFindings(&snapshot)

	if args.deepInspect {
		declSecrets, declVars, diWarnings, err := deepInspectDeclared(ctx, gh, cfg.Targets, repos, snapshot.GitHubWebBase)
		if err != nil {
			fmt.Fprintf(stderr, "warning: deep inspection failed: %v\n", err)
		}
		snapshot.DeclaredSecrets = declSecrets
		snapshot.DeclaredVariables = declVars
		markDeclaredUsed(&snapshot)
		if len(diWarnings) > 0 {
			snapshot.DeepInspectWarnings = append(snapshot.DeepInspectWarnings, diWarnings...)
			for _, w := range diWarnings {
				fmt.Fprintf(stderr, "warning: %s\n", w)
			}
		}
	}

	snapshotPath := filepath.Join(args.outDir, "snapshot.json")
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(snapshotPath, b, 0o644); err != nil {
		return err
	}

	htmlPath := filepath.Join(args.outDir, "report.html")
	if err := report.WriteHTML(htmlPath, snapshot); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "wrote %s\n", snapshotPath)
	fmt.Fprintf(stdout, "wrote %s\n", htmlPath)
	return nil
}

func deepInspectDeclared(ctx context.Context, gh *githubclient.Client, targets config.Targets, repos []*github.Repository, webBase string) ([]model.DeclaredItem, []model.DeclaredItem, []string, error) {
	base := strings.TrimRight(strings.TrimSpace(webBase), "/")
	if base == "" {
		base = "https://github.com"
	}

	warnings := make([]string, 0)
	addWarn := func(msg string) {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			return
		}
		warnings = append(warnings, msg)
	}
	permWarned := map[string]struct{}{}
	addPermWarnOnce := func(key, msg string) {
		if _, ok := permWarned[key]; ok {
			return
		}
		permWarned[key] = struct{}{}
		addWarn(msg)
	}
	statusCode := func(err error) int {
		var er *github.ErrorResponse
		if errors.As(err, &er) {
			if er.Response != nil {
				return er.Response.StatusCode
			}
		}
		return 0
	}
	isPermErr := func(err error) bool {
		sc := statusCode(err)
		return sc == 403 || sc == 404
	}

	declSecrets := make([]model.DeclaredItem, 0)
	declVars := make([]model.DeclaredItem, 0)

	// Org scope (only for explicit org targets)
	for _, org := range targets.Orgs {
		org = strings.TrimSpace(org)
		if org == "" {
			continue
		}
		secrets, err := gh.ListOrgSecretNames(ctx, org)
		if err != nil {
			if isPermErr(err) {
				addWarn(fmt.Sprintf("deep-inspect: insufficient permissions to list org secrets for %s (HTTP %d). See permissions.md to grant Actions secrets read.", org, statusCode(err)))
				continue
			}
			addWarn(fmt.Sprintf("deep-inspect: failed to list org secrets for %s: %v", org, err))
			continue
		}
		for _, name := range secrets {
			declSecrets = append(declSecrets, model.DeclaredItem{
				Name:      name,
				ScopeKind: "org",
				Org:       org,
				ManageURL: fmt.Sprintf("%s/organizations/%s/settings/secrets/actions", base, org),
			})
		}

		vars, err := gh.ListOrgVariableNames(ctx, org)
		if err != nil {
			if isPermErr(err) {
				addWarn(fmt.Sprintf("deep-inspect: insufficient permissions to list org variables for %s (HTTP %d). See permissions.md to grant Actions variables read.", org, statusCode(err)))
				continue
			}
			addWarn(fmt.Sprintf("deep-inspect: failed to list org variables for %s: %v", org, err))
			continue
		}
		for _, name := range vars {
			declVars = append(declVars, model.DeclaredItem{
				Name:      name,
				ScopeKind: "org",
				Org:       org,
				ManageURL: fmt.Sprintf("%s/organizations/%s/settings/variables/actions", base, org),
			})
		}
	}

	// Repo + environment scopes per repo
	for _, r := range repos {
		owner := r.GetOwner().GetLogin()
		repo := r.GetName()
		if owner == "" || repo == "" {
			continue
		}

		repoSecrets, err := gh.ListRepoSecretNames(ctx, owner, repo)
		if err != nil {
			if isPermErr(err) {
				addWarn(fmt.Sprintf("deep-inspect: insufficient permissions to list repo secrets for %s/%s (HTTP %d). See permissions.md to grant Actions secrets read.", owner, repo, statusCode(err)))
			} else {
				addWarn(fmt.Sprintf("deep-inspect: failed to list repo secrets for %s/%s: %v", owner, repo, err))
			}
		} else {
			for _, name := range repoSecrets {
				declSecrets = append(declSecrets, model.DeclaredItem{
					Name:      name,
					ScopeKind: "repo",
					RepoOwner: owner,
					RepoName:  repo,
					ManageURL: fmt.Sprintf("%s/%s/%s/settings/secrets/actions", base, owner, repo),
				})
			}
		}

		repoVars, err := gh.ListRepoVariableNames(ctx, owner, repo)
		if err != nil {
			if isPermErr(err) {
				addWarn(fmt.Sprintf("deep-inspect: insufficient permissions to list repo variables for %s/%s (HTTP %d). See permissions.md to grant Actions variables read.", owner, repo, statusCode(err)))
			} else {
				addWarn(fmt.Sprintf("deep-inspect: failed to list repo variables for %s/%s: %v", owner, repo, err))
			}
		} else {
			for _, name := range repoVars {
				declVars = append(declVars, model.DeclaredItem{
					Name:      name,
					ScopeKind: "repo",
					RepoOwner: owner,
					RepoName:  repo,
					ManageURL: fmt.Sprintf("%s/%s/%s/settings/variables/actions", base, owner, repo),
				})
			}
		}

		envs, err := gh.ListEnvironments(ctx, owner, repo)
		if err != nil {
			if isPermErr(err) {
				addPermWarnOnce(
					"env|"+owner+"/"+repo,
					fmt.Sprintf("deep-inspect: insufficient permissions to interrogate environments for %s/%s (HTTP %d). This prevents enumerating environment-scoped secrets/vars. See permissions.md and grant access to environments.", owner, repo, statusCode(err)),
				)
				continue
			}
			addWarn(fmt.Sprintf("deep-inspect: failed to list environments for %s/%s: %v", owner, repo, err))
			continue
		}
		for _, env := range envs {
			envName := strings.TrimSpace(env.Name)
			if envName == "" {
				continue
			}
			manageURL := fmt.Sprintf("%s/%s/%s/settings/environments", base, owner, repo)
			if env.ID > 0 {
				manageURL = fmt.Sprintf("%s/%s/%s/settings/environments/%d/edit", base, owner, repo, env.ID)
			}

			envSecrets, err := gh.ListEnvironmentSecretNames(ctx, owner, repo, envName)
			if err != nil {
				if isPermErr(err) {
					addPermWarnOnce(
						"env|"+owner+"/"+repo,
						fmt.Sprintf("deep-inspect: insufficient permissions to interrogate environments for %s/%s (HTTP %d). This prevents enumerating environment-scoped secrets/vars. See permissions.md and grant access to environments.", owner, repo, statusCode(err)),
					)
				} else {
					addWarn(fmt.Sprintf("deep-inspect: failed to list environment secrets for %s/%s env %q: %v", owner, repo, envName, err))
				}
			} else {
				for _, name := range envSecrets {
					declSecrets = append(declSecrets, model.DeclaredItem{
						Name:        name,
						ScopeKind:   "environment",
						RepoOwner:   owner,
						RepoName:    repo,
						Environment: envName,
						ManageURL:   manageURL,
					})
				}
			}

			envVars, err := gh.ListEnvironmentVariableNames(ctx, owner, repo, envName)
			if err != nil {
				if isPermErr(err) {
					addPermWarnOnce(
						"env|"+owner+"/"+repo,
						fmt.Sprintf("deep-inspect: insufficient permissions to interrogate environments for %s/%s (HTTP %d). This prevents enumerating environment-scoped secrets/vars. See permissions.md and grant access to environments.", owner, repo, statusCode(err)),
					)
				} else {
					addWarn(fmt.Sprintf("deep-inspect: failed to list environment variables for %s/%s env %q: %v", owner, repo, envName, err))
				}
			} else {
				for _, name := range envVars {
					declVars = append(declVars, model.DeclaredItem{
						Name:        name,
						ScopeKind:   "environment",
						RepoOwner:   owner,
						RepoName:    repo,
						Environment: envName,
						ManageURL:   manageURL,
					})
				}
			}
		}
	}

	sort.SliceStable(declSecrets, func(i, j int) bool {
		a := declSecrets[i]
		b := declSecrets[j]
		ka := a.ScopeKind + "|" + a.Org + "|" + a.RepoOwner + "/" + a.RepoName + "|" + a.Environment + "|" + a.Name
		kb := b.ScopeKind + "|" + b.Org + "|" + b.RepoOwner + "/" + b.RepoName + "|" + b.Environment + "|" + b.Name
		return ka < kb
	})
	sort.SliceStable(declVars, func(i, j int) bool {
		a := declVars[i]
		b := declVars[j]
		ka := a.ScopeKind + "|" + a.Org + "|" + a.RepoOwner + "/" + a.RepoName + "|" + a.Environment + "|" + a.Name
		kb := b.ScopeKind + "|" + b.Org + "|" + b.RepoOwner + "/" + b.RepoName + "|" + b.Environment + "|" + b.Name
		return ka < kb
	})

	sort.Strings(warnings)
	if len(warnings) > 1 {
		out := warnings[:0]
		var last string
		for i, w := range warnings {
			if i == 0 || w != last {
				out = append(out, w)
				last = w
			}
		}
		warnings = out
	}
	return declSecrets, declVars, warnings, nil
}

func markDeclaredUsed(snap *model.Snapshot) {
	secretEnv := map[string]int{}
	secretRepo := map[string]int{}
	secretOrg := map[string]int{}
	secretCandidates := map[string][]int{}

	for i := range snap.DeclaredSecrets {
		d := snap.DeclaredSecrets[i]
		switch d.ScopeKind {
		case "environment":
			secretEnv[d.RepoOwner+"/"+d.RepoName+"|"+d.Environment+"|"+d.Name] = i
		case "repo":
			secretRepo[d.RepoOwner+"/"+d.RepoName+"|"+d.Name] = i
		case "org":
			secretOrg[d.Org+"|"+d.Name] = i
		}
		secretCandidates[d.Name] = append(secretCandidates[d.Name], i)
	}

	varEnv := map[string]int{}
	varRepo := map[string]int{}
	varOrg := map[string]int{}
	varCandidates := map[string][]int{}

	for i := range snap.DeclaredVariables {
		d := snap.DeclaredVariables[i]
		switch d.ScopeKind {
		case "environment":
			varEnv[d.RepoOwner+"/"+d.RepoName+"|"+d.Environment+"|"+d.Name] = i
		case "repo":
			varRepo[d.RepoOwner+"/"+d.RepoName+"|"+d.Name] = i
		case "org":
			varOrg[d.Org+"|"+d.Name] = i
		}
		varCandidates[d.Name] = append(varCandidates[d.Name], i)
	}

	mark := func(items []model.DeclaredItem, idx int, count int) {
		if idx < 0 || idx >= len(items) {
			return
		}
		items[idx].UsedCount += count
		items[idx].Used = items[idx].UsedCount > 0
	}

	markSecretIdx := func(idx int, count int) {
		if idx < 0 || idx >= len(snap.DeclaredSecrets) {
			return
		}
		snap.DeclaredSecrets[idx].UsedCount += count
		snap.DeclaredSecrets[idx].Used = snap.DeclaredSecrets[idx].UsedCount > 0
	}
	markVarIdx := func(idx int, count int) {
		if idx < 0 || idx >= len(snap.DeclaredVariables) {
			return
		}
		snap.DeclaredVariables[idx].UsedCount += count
		snap.DeclaredVariables[idx].Used = snap.DeclaredVariables[idx].UsedCount > 0
	}

	for _, mf := range snap.MergedFindings {
		if mf.RefType != "secret" && mf.RefType != "var" {
			continue
		}
		name := mf.RefName
		repoKey := mf.RepoOwner + "/" + mf.RepoName

		envs := map[string]struct{}{}
		for _, c := range mf.Contexts {
			e := strings.TrimSpace(c.Environment)
			if e == "" {
				continue
			}
			envs[e] = struct{}{}
		}
		envName := ""
		if len(envs) == 1 {
			for e := range envs {
				envName = e
			}
		}

		if mf.RefType == "secret" {
			if envName != "" {
				if idx, ok := secretEnv[repoKey+"|"+envName+"|"+name]; ok {
					markSecretIdx(idx, mf.Count)
					continue
				}
			}
			if idx, ok := secretRepo[repoKey+"|"+name]; ok {
				markSecretIdx(idx, mf.Count)
				continue
			}
			if idx, ok := secretOrg[mf.RepoOwner+"|"+name]; ok {
				markSecretIdx(idx, mf.Count)
				continue
			}

			cands := secretCandidates[name]
			if len(cands) == 1 {
				markSecretIdx(cands[0], mf.Count)
				continue
			}
			for _, idx := range cands {
				markSecretIdx(idx, mf.Count)
			}
		}

		if mf.RefType == "var" {
			if envName != "" {
				if idx, ok := varEnv[repoKey+"|"+envName+"|"+name]; ok {
					markVarIdx(idx, mf.Count)
					continue
				}
			}
			if idx, ok := varRepo[repoKey+"|"+name]; ok {
				markVarIdx(idx, mf.Count)
				continue
			}
			if idx, ok := varOrg[mf.RepoOwner+"|"+name]; ok {
				markVarIdx(idx, mf.Count)
				continue
			}

			cands := varCandidates[name]
			if len(cands) == 1 {
				markVarIdx(cands[0], mf.Count)
				continue
			}
			for _, idx := range cands {
				markVarIdx(idx, mf.Count)
			}
		}
	}

	_ = mark
}
