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
	"time"

	"gopkg.in/yaml.v3"

	"secret-inventory/internal/analyze"
	"secret-inventory/internal/config"
	"secret-inventory/internal/githubclient"
	"secret-inventory/internal/model"
	"secret-inventory/internal/report"
)

type scanArgs struct {
	configPath string
	outDir     string
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

	ctx := context.Background()
	gh := githubclient.New(token, cfg.GitHub.BaseURL, etagStore)

	repos, err := gh.ResolveTargets(ctx, cfg.Targets)
	if err != nil {
		return err
	}

	snapshot := model.Snapshot{
		GeneratedAt: time.Now().UTC(),
		Targets:     cfg.Targets,
		Repos:       make([]model.Repo, 0, len(repos)),
		Findings:    []model.Finding{},
	}

	scanner := analyze.NewScanner(analyze.ScannerOptions{
		ScriptExtensions: cfg.Scanner.ScriptExtensions,
		MaxFileBytes:     cfg.Scanner.MaxFileBytes,
		IncludeUnknown:   cfg.Scanner.IncludeUnknownEnv,
	})

	for _, r := range repos {
		snapshot.Repos = append(snapshot.Repos, model.Repo{
			Owner:         r.GetOwner().GetLogin(),
			Name:          r.GetName(),
			DefaultBranch: r.GetDefaultBranch(),
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
