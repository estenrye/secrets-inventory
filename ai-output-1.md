# Golang CLI generated (from `proposal2.md`)

## What was built
A **runnable Go CLI** implementing the `scan` flow:

- **Repo target discovery** for:
  - `targets.orgs`
  - `targets.users`
  - explicit `targets.repos`
- **Workflow discovery** via repo contents:
  - lists `.github/workflows/*.yml|*.yaml`
- **Workflow scanning**
  - extracts `secrets.NAME`, `vars.NAME`, `env.NAME`
  - identifies repo-local scripts referenced in `run:` (conservative `./...` patterns)
  - identifies **local actions** `uses: ./...` and fetches `action.yml|action.yaml`
  - for composite actions: scans `runs.steps[*].run` and follows `./script` references
  - for node actions: follows `runs.main` entrypoint and scans it (if extension allowed)
- **Script scanning**
  - finds `$NAME` / `${NAME}` occurrences as `runtime_env`
  - suppresses unknown env names by default unless `scanner.include_unknown_env: true`
  - marks env names as “known” when declared in workflow/job/step `env:` blocks or referenced via `env.NAME` expressions
- **Outputs**
  - `out/snapshot.json`
  - `out/report.html`
- **ETag caching support**
  - stores ETags in `out/.cache/etags.json`
  - uses `If-None-Match` when refetching files

## Files added
- **Go module**
  - `go.mod`
- **CLI entrypoint**
  - `cmd/secret-inventory/main.go`
- **Packages**
  - `internal/config/config.go`
  - `internal/model/model.go`
  - `internal/githubclient/client.go`
  - `internal/githubclient/etag.go`
  - `internal/analyze/scanner.go`
  - `internal/report/report.go`
- **Example config**
  - `config.example.yml`

## How to run
1. Create a config:
   - Copy `config.example.yml` to `config.yml` and edit targets.

2. Provide a token (recommended via env):
   - `export GITHUB_TOKEN=...`
   - Use a **fine-grained PAT** with **Contents: Read** to the repos you want scanned.

3. Run scan:

```sh
go run ./cmd/secret-inventory scan --config config.yml --out out
```

Outputs:
- `out/snapshot.json`
- `out/report.html`

## Notes / current limitations (intentional for MVP)
- Script discovery only follows `./relative/path` patterns found in `run:` and composite action `run:` blocks.
- No org/repo secret/variable **name** enrichment yet (API calls for that are not implemented).
- No policy engine/diff/alerts yet—this is a working scanner + report foundation aligned to proposal2’s scanning expansion.
