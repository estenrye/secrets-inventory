# ai-output-4.md

## Objective
Resolve why `secret-inventory scan` produced an HTML report with **0 findings** after scanning many repositories, and document the enhancements and fixes that made the report correctly display results.

## Symptom
- Running:
  - `./bin/secret-inventory scan --config ~/.config/secret-inventory.yml --out ~/secret-inventory`
- Produced:
  - `~/secret-inventory/snapshot.json` with `"findings": []`
  - `~/secret-inventory/report.html` with empty tables (Secrets / Env / Vars / Runtime env)

## Initial diagnosis
The report generator was functioning correctly and rendering from `snapshot.json`.

The immediate cause of the empty report was that **the scanner emitted an empty `findings` array**, so the report had nothing to display.

## Enhancements implemented
### 1) Workflow YAML: always perform a raw-text reference scan
File: `internal/analyze/scanner.go`

Change:
- The workflow scanner now **always** runs a raw-text scan across the full workflow YAML text (even when YAML parsing succeeds).

Why:
- The YAML AST walk is best-effort and only visits a subset of fields.
- References to `secrets.*`, `vars.*`, or `env.*` can exist in fields not covered by the AST walk.
- Previously, the raw-text scan only ran when YAML parsing failed entirely.

Impact:
- The scanner can now detect references anywhere in the workflow file.

### 2) Workflow YAML: workflow-level `env:` support
File: `internal/analyze/scanner.go`

Change:
- Added scanning of top-level workflow `env:` keys/values.

Why:
- Workflow-level `env` declarations should be treated as “known env vars” so that script scanning can properly classify runtime env usage.

### 3) Finding deduplication
File: `internal/analyze/scanner.go`

Change:
- Added deduplication logic to prevent duplicates between:
  - AST-walk based extraction
  - full-file raw-text extraction

Why:
- After enabling both extraction approaches, the same finding can be discovered twice.

## Root-cause bug fix
### 4) ETag conditional requests: handle `304 Not Modified` correctly by caching file contents
Files:
- `internal/githubclient/client.go`
- `internal/githubclient/filecache.go` (new)
- `cmd/secret-inventory/main.go`

Problem:
- The GitHub client uses ETags (`If-None-Match`) to reduce API usage.
- When GitHub responds with `304 Not Modified`, the previous implementation returned `ErrNotModified` and **skipped scanning**.
- Because there was **no local content cache**, a `304` effectively meant “no content available to scan”, which could lead to persistent `findings: []` even when workflows existed.

Fix:
- Implemented a disk-backed file content cache (`FileCache`).
- On `200 OK`, store content in the cache.
- On `304 Not Modified`, return the cached content so the scanner can still analyze the file.
- Wired the cache directory to the scan output directory: `out/.cache/files/`.

## User verification
The user verified the fix by:
- Removing the previous output directory.
- Re-running `scan` into a fresh output directory.
- Observing that the report began displaying results (non-empty `findings` in `snapshot.json`).

## Notes / follow-ups observed during scanning
- Many repositories returned `404 Not Found` for `.github/workflows`, indicating no workflows in those repositories.
- Some repositories produced warnings for unresolved local-action/script paths containing placeholders such as:
  - `__BUILDER_CHECKOUT_DIR__`
  - `__THIS_REPO__`
  These are not real repository paths and should be treated as non-fetchable.
- Some large files may require a different download path if GitHub returns an unsupported encoding due to size.

## Outcome
- The scan pipeline now reliably produces findings when workflows contain references.
- `snapshot.json` and `report.html` correctly display categorized findings.
