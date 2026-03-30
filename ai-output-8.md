# ai-output-8.md

## Objective
Document the interaction and implementation work for **Proposal v7** (`proposal7.md`): add **permission warnings** when repositories return **HTTP 403** during core scanning of:

- `.github/workflows/*` workflow files
- referenced scripts
- local actions (and their referenced scripts)

The feature adds a `--verbose` mode to stream warnings as they occur, persists warnings into `snapshot.json`, and renders a grouped warning banner in `report.html`.

---

## What you requested
- Re-read all `*.md` files for context refresh.
- Write **Proposal v7** to add logging/warnings for repos returning **403** when trying to read Actions/workflows and scripts.
- Record decisions for Proposal v7 open questions.
- Implement Proposal v7 in the Go CLI.
- Document the Proposal v7 interactions/changes in `ai-output-8.md`.

---

## Key decisions (made by you)

### 1) Console printing behavior
- **Default**: collect and print warnings **once at the end** of the scan (grouped/deduped).
- **Verbose**: when `--verbose` is passed, also print warnings **immediately** as they occur.

### 2) 403 vs 404 semantics
- **403 Forbidden**: treated as an actionable permission issue; warn and continue best-effort.
- **404 Not Found**: do **not** treat as a permission warning.
  - `404` for `.github/workflows` generally means “no workflows”.
  - `404` for a referenced script/action path generally means “path not present”.

---

## Proposal v7 written
File created:
- `proposal7.md`

Proposal content included:
- detection points (workflow discovery, workflow fetch, script/local-action fetch)
- warning normalization + dedupe rules
- recommendation to persist warnings into snapshot and render a report banner
- UX details for comma-delimited repo lists with individual `<code>` formatting

Later edits (per your answers) updated:
- the Open Questions section
- edge-case guidance
- implementation plan language (403-only classifier; 404 distinct)

---

## Implementation changes applied

### 1) Snapshot model updates
File: `internal/model/model.go`
- Added `Snapshot.ScanWarnings []ScanWarning` (`scan_warnings` in JSON)
- Added `ScanWarning` struct:
  - `kind`, `repo_owner`, `repo_name`
  - `http_status`, `operation`, `path`
  - `message` (grouping key)

### 2) CLI updates (core scanning warnings)
File: `cmd/secret-inventory/main.go`

#### A) New CLI flag
- Added `--verbose` to `scan`.
- Updated `usage()` output accordingly.

#### B) Warning collection + dedupe
- Implemented a repo+kind dedupe map (`warnByRepo[owner/repo][kind]`).
- Warning kinds implemented:
  - `workflow_read_forbidden`
  - `script_read_forbidden`
  - `local_action_read_forbidden`

#### C) 403 detection points (core scan path)
- Workflow directory listing (`ListWorkflowFiles`):
  - `403` => record `workflow_read_forbidden`.
  - `404` => treat as “no workflows”, no warning.
- Workflow file fetch (`GetFile` for workflow path):
  - `403` => record `workflow_read_forbidden`.
- Script/local-action fetch (`GetFile` for additional file refs):
  - `403` => record `script_read_forbidden` or `local_action_read_forbidden` based on `FileRef.Kind`.

#### D) Console printing behavior
- If `--verbose` is set:
  - print warning immediately when first observed for repo/kind.
- Always:
  - at end of scan, group by `message` and print one consolidated warning per message with a comma-delimited repo list.

#### E) Snapshot persistence
- Appended collected warnings into `snapshot.scan_warnings` before writing `snapshot.json`.

### 3) HTML report updates
File: `internal/report/report.go`

- Added grouping for `snapshot.scan_warnings` by `message`.
- Rendered a **Scan warnings** banner near the top of the report:
  - grouped by `Message`
  - repo lists rendered as comma-delimited entries with each repo individually wrapped in `<code>`

The Scan warnings banner is rendered **above** the existing Deep inspection warnings banner.

---

## Verification
- Built successfully:
  - `make build`

Recommended runtime verification:
- Run a scan that includes at least one repo that triggers a **403** during contents reads.
- Confirm:
  - warnings appear in stderr (end-of-scan grouping)
  - `--verbose` streams warnings as they occur
  - `snapshot.json` includes `scan_warnings`
  - `report.html` includes the Scan warnings banner with grouped warnings and correct repo list formatting.

---

## Outcome
- The CLI now surfaces **actionable, deduped** permission warnings for **403** failures during core scanning.
- Warnings are present in:
  - stderr (grouped; immediate with `--verbose`)
  - `snapshot.json` (`scan_warnings`)
  - `report.html` (Scan warnings banner)

---

## Follow-ups
- Extend `tools/verify_links.py` to assert `scan_warnings` and the Scan warnings banner exist when expected.
