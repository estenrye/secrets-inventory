package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	configPath string
	outDir     string
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
	return "Usage:\n  secret-inventory scan --config <config.yml> --out <out-dir>\n"
}

func parseScanArgs(argv []string) (scanArgs, error) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var a scanArgs
	fs.StringVar(&a.configPath, "config", "", "path to config yaml")
	fs.StringVar(&a.outDir, "out", "out", "output directory")
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

	scanner := analyze.NewScanner(analyze.ScannerOptions{
		ScriptExtensions: cfg.Scanner.ScriptExtensions,
		MaxFileBytes:     cfg.Scanner.MaxFileBytes,
		IncludeUnknown:   cfg.Scanner.IncludeUnknownEnv,
	})

	for _, r := range repos {
		sha, shaErr := gh.DefaultBranchSHA(ctx, r)
		if shaErr != nil {
			fmt.Fprintf(os.Stderr, "warning: %s/%s: unable to resolve default branch SHA: %v\n", r.GetOwner().GetLogin(), r.GetName(), shaErr)
		}
		snapshot.Repos = append(snapshot.Repos, model.Repo{
			Owner:         r.GetOwner().GetLogin(),
			Name:          r.GetName(),
			DefaultBranch: r.GetDefaultBranch(),
			ScannedRef:    sha,
			Archived:      r.GetArchived(),
			Private:       r.GetPrivate(),
		})

		workflowFiles, err := gh.ListWorkflowFiles(ctx, r)
		if err != nil {
			fmt.Fprintf(stderr, "warning: %s/%s: %v\n", r.GetOwner().GetLogin(), r.GetName(), err)
			continue
		}

		for _, wf := range workflowFiles {
			wfContent, meta, err := gh.GetFile(ctx, r, wf)
			if err != nil {
				if errors.Is(err, githubclient.ErrNotModified) {
					continue
				}
				fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", r.GetOwner().GetLogin(), r.GetName(), wf, err)
				continue
			}

			wfFindings, additionalFiles, err := scanner.ScanWorkflowYAML(r.GetOwner().GetLogin(), r.GetName(), wf, wfContent)
			if err != nil {
				fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", r.GetOwner().GetLogin(), r.GetName(), wf, err)
			}
			snapshot.Findings = append(snapshot.Findings, wfFindings...)

			for _, f := range additionalFiles {
				content, _, err := gh.GetFile(ctx, r, f.Path)
				if err != nil {
					if errors.Is(err, githubclient.ErrNotModified) {
						continue
					}
					fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", r.GetOwner().GetLogin(), r.GetName(), f.Path, err)
					continue
				}
				fileFindings, moreFiles, err := scanner.ScanRepoFile(f, content)
				if err != nil {
					fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", r.GetOwner().GetLogin(), r.GetName(), f.Path, err)
				}
				snapshot.Findings = append(snapshot.Findings, fileFindings...)

				for _, mf := range moreFiles {
					c2, _, err := gh.GetFile(ctx, r, mf.Path)
					if err != nil {
						if errors.Is(err, githubclient.ErrNotModified) {
							continue
						}
						fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", r.GetOwner().GetLogin(), r.GetName(), mf.Path, err)
						continue
					}
					ff2, _, err := scanner.ScanRepoFile(mf, c2)
					if err != nil {
						fmt.Fprintf(stderr, "warning: %s/%s %s: %v\n", r.GetOwner().GetLogin(), r.GetName(), mf.Path, err)
					}
					snapshot.Findings = append(snapshot.Findings, ff2...)
				}
			}

			gh.StoreETag(meta)
		}
	}

	if err := gh.SaveETags(etagPath); err != nil {
		fmt.Fprintf(stderr, "warning: failed to save etag cache: %v\n", err)
	}

	snapshot.MergedFindings = mergeFindings(&snapshot)

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
