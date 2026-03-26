# ai-output-5.md

## Objective
Document the interaction and implementation work for **Proposal v4** (`proposal4.md`): adding **line-level GitHub hyperlinks** to findings in the `secret-inventory` HTML report.

## Context review
- Reviewed existing project docs to ensure Proposal v4 aligned with the current architecture and data model:
  - `spec.md` (baseline requirements for secrets/env usage inventory)
  - `proposal.md`, `proposal2.md`, `proposal3.md` (scanner/report evolution)
  - `permissions.md`, `references.md` (permissions and terminology)
  - `ai-output-1.md` .. `ai-output-4.md` (prior work logs)

## Proposal v4 created
Created `proposal4.md` describing how to provide:
- **Stable permalinks** to GitHub source using commit SHAs.
- **Exact line anchors** (`#L<line>`) for each finding.
- Data model extensions needed to support links.

## Decisions captured during discussion
### 1) Commit SHA permalinks
- Decision: use **commit SHA permalinks** wherever possible.
- Fallback: default branch only if SHA cannot be resolved.

### 2) Link precision
- Decision: link precision must be **exact line**.
- Note: GitHub supports line anchors but not substring anchors.

### 3) Placeholder local paths (Option 1)
We discussed placeholders like:
- `__THIS_REPO__`
- `__BUILDER_CHECKOUT_DIR__`

These are not real repository paths and often yield `404` when fetching content.

- Decision: use **Option 1**
  - Suppress hyperlink generation for these placeholder paths.
  - Render `Source: n/a` in the report for those rows.

## Implementation applied to the CLI
### 1) Snapshot / model changes
File: `internal/model/model.go`
- Added `Snapshot.GitHubWebBase` (`github_web_base` in JSON)
- Added `Repo.ScannedRef` (`scanned_ref` in JSON)
- Added finding location fields:
  - `line_start`, `col_start`, `line_end`, `col_end`

### 2) GitHub client enrichment
File: `internal/githubclient/client.go`
- Added `DefaultBranchSHA(ctx, repo)` helper to resolve default-branch HEAD commit SHA.

### 3) CLI scan pipeline updates
File: `cmd/secret-inventory/main.go`
- Added derivation of a GitHub web base URL:
  - default: `https://github.com`
  - GHES mapping: trims `/api/v3` from `config.github.base_url`
- Populated `snapshot.github_web_base`.
- Populated each `Repo.scanned_ref` from `DefaultBranchSHA`.

### 4) Scanner line attribution
File: `internal/analyze/scanner.go`
- Implemented a small line index helper (`lineIndex`) to map match indices to `(line, col)`.
- Updated YAML walkers (`walkWorkflow`, `walkActionYAML`, `yamlWalkStrings`) to propagate YAML scalar node `Line` and `Column`.
- Updated reference scanning (`scanStringForRefs`) so each `secrets.*` / `vars.*` / `env.*` match gets `line_start` / `col_start`.
- Script scanning (`runtime_env`) also records line/col for `$NAME` / `${NAME}` matches.

### 5) HTML report hyperlinking
File: `internal/report/report.go`
- Added a **Source** column containing a `view` link.
- Built links as:
  - `<github_web_base>/<owner>/<repo>/blob/<scanned_ref>/<path>#L<line_start>`
- Implemented placeholder suppression (Option 1):
  - if path contains `__THIS_REPO__` or `__BUILDER_CHECKOUT_DIR__`, render `n/a`.

## Verification (on system constraints)
### Python usage note
- The environment does **not** provide `python` on PATH; verification must use **`python3`**.

### Scan verification
- Built the CLI and ran a scan producing output in `~/secret-inventory-links`.

Verified using `python3` that:
- `github_web_base` is present.
- Every repo has `scanned_ref`.
- Every finding has `line_start`.
- `report.html` includes a `Source` column and `blob/<sha>/...#L...` links.

### Verification tool added to repo
Added reusable script:
- `tools/verify_links.py`

Usage:
- `python3 tools/verify_links.py --out ~/secret-inventory-links`

## Outcome
- The CLI now emits snapshots with the metadata required to generate stable, line-level GitHub permalinks.
- The HTML report renders a **Source** link per finding.
- Placeholder-derived paths are handled safely using **Option 1** (no broken links).
