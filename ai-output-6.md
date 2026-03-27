# ai-output-6.md

## Objective
Document the interaction and implementation work for **Proposal v5** (`proposal5.md`): using **GitHub source links** (repo + scanned ref + file path + line) to **deduplicate and merge secret findings**, producing cleaner snapshots and a more usable HTML report.

---

## Context review (docs)
Before implementing, we re-read the project docs to ensure Proposal v5 aligned with the existing architecture and prior decisions:

- `spec.md` (baseline requirements)
- `proposal.md`, `proposal2.md`, `proposal3.md`, `proposal4.md` (scanner/report evolution)
- `permissions.md`, `references.md` (permissions and terminology)
- `ai-output-1.md` .. `ai-output-5.md` (prior work logs)

Key prior context that Proposal v5 builds on:
- Proposal v4 added **line-level GitHub permalinks** to findings using per-repo `scanned_ref`.
- The scanner intentionally runs multiple passes (AST + raw scan + script scanning), which can produce duplicates.

---

## Decisions made during Proposal v5 discussion
### 1) Representation of merged findings
- Decision: choose **(B)**
  - Introduce a new `MergedFinding` structure with an explicit `contexts` list.
  - Keep the legacy `findings` list for compatibility.

### 2) Cross-workflow dedupe
- Decision: **yes**
  - If multiple workflows point to the same underlying source location (same repo+ref+path+line), merge them.
  - Preserve originating workflow contexts inside the merged finding.

### 3) Placeholder paths
- Reuse Proposal v4 decision (Option 1)
  - Paths containing `__THIS_REPO__` or `__BUILDER_CHECKOUT_DIR__` are not linkable.
  - Suppress hyperlink generation for them.
  - For dedupe, these fall back to contextual keys (do not source-link dedupe).

---

## Implementation changes applied

### 1) Snapshot / model changes
File: `internal/model/model.go`

Added schema to support merged rows:
- `Snapshot.MergedFindings []MergedFinding` (`merged_findings` in JSON)
- `Finding.SourceKey string` (`source_key` in JSON)

Added new types:
- `FindingContext`
  - Captures a usage context for a finding (workflow/job/step/field/context_kind/action_uses/origin)
- `MergedFinding`
  - Holds the stable finding identity fields plus:
    - `count` (# of merged rows)
    - `contexts` (all contributing contexts)

Note:
- `MergedFinding.workflow_path` is optional because merged rows can span multiple workflows; contexts contain the authoritative workflow attribution.

### 2) CLI scan pipeline: build merged findings
File: `cmd/secret-inventory/main.go`

Added a post-scan merge step:
- `snapshot.MergedFindings = mergeFindings(&snapshot)`

Source-key computation:
- Uses per-repo `scanned_ref` (commit SHA) when available, otherwise default branch.
- `source_key` format:
  - `<owner>/<repo>@<sha>:<path>#L<line_start>`
- The file `path` is chosen based on `file_kind`:
  - `workflow_yaml` → `workflow_path`
  - otherwise → `file_path`
- `source_key` is only computed when:
  - `line_start > 0`
  - ref is known
  - path is non-empty
  - path is not a placeholder token path

Grouping / merge rules:
- Group key:
  - primary: `source_key` (when available)
  - fallback: a stable contextual key (includes repo/workflow/job/step/field/file/context/line/col)
  - plus: `|<ref_type>.<ref_name>` to avoid merging different refs found on the same line.

Representative selection (important UX requirement):
- When merging, preserve populated values:
  - If one row has a value and another row lacks it, keep the populated value in the merged representative.
  - Implemented as field-by-field “fill from other items if empty/zero”.

Important bug fix discovered during implementation:
- The merge function originally took the snapshot by value, so `source_key` updates weren’t persisted into `snapshot.findings`.
- Fixed by changing `mergeFindings(snap *model.Snapshot)` and calling with `&snapshot`.

### 3) HTML report updates
File: `internal/report/report.go`

Behavior:
- Prefer merged display when `snapshot.merged_findings` is present.
- Otherwise fall back to the original per-finding tables.

Merged-table UX iterations:
- Job/Step columns were initially removed; user requested they be restored.
- Added Job and Step columns back.
- User did not want the string `multiple`.
  - Updated merged Job/Step to select a single best value:
    - first non-empty `job_id` / `step_name` from contexts.

Contexts column iterations:
- First: showed only the first context kind.
- Then: showed all contexts (full details).
- Final: user requested **only context kinds**.
  - Contexts column now shows only unique `context_kind` values, one per line, in first-seen order.

Template correctness:
- Fixed an HTML template structure issue where a table was not properly closed after inserting the merged table block.

### 4) Verification tooling updated
File: `tools/verify_links.py`

Enhanced to check:
- `findings_with_source_key`
- presence and basic structure of `merged_findings`
- report includes `Count` column

---

## User verification steps
The user verified end-to-end behavior by:

- Building:
  - `make build`
- Running scans:
  - `./bin/secret-inventory scan --config ~/.config/secret-inventory.yml --out ~/secret-inventory-links`

Observed:
- `snapshot.json` now includes `merged_findings` entries with:
  - `source_key`
  - `count`
  - `contexts` list
- `report.html` renders merged rows with:
  - Job
  - Step
  - Contexts (context kinds)
  - Source links

Notes:
- Warnings for placeholder paths (`__THIS_REPO__`, `__BUILDER_CHECKOUT_DIR__`) remain expected and are intentionally treated as non-linkable.

---

## Outcome
- Proposal v5 is implemented:
  - Source-link based dedupe/merge is available via `merged_findings`.
  - Snapshot remains backward compatible via `findings`.
  - HTML report defaults to the merged view when available.
  - Column behavior was tuned based on user feedback (Job/Step restored; no `multiple`; contexts show context kinds only).
