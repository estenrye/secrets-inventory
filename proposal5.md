# Proposal v5: Deduplicate and merge findings using GitHub source links

## Summary
Now that each finding can be linked to GitHub at an exact **file + line** (Proposal v4), we can use that source identity to:

- **Deduplicate** multiple findings that represent the same underlying occurrence.
- **Merge data** from duplicate rows into a single “merged finding”, preserving all useful context.
- Reduce report noise, especially when findings are discovered via multiple extraction passes (AST + raw scan) or via multiple contexts that point to the same source line.

This proposal focuses on *dedup/merge at the data layer* (`snapshot.json`), with optional additional report grouping.

---

## Background / problem
The scanner intentionally uses multiple strategies:
- YAML AST walk for context-aware extraction
- Raw-text scanning across full files to catch fields the walker doesn’t traverse
- Script/local-action scanning

Even with the existing dedupe logic (keyed on repo/workflow/job/step/field/context/etc.), it is still possible to produce noisy duplicates, e.g.:
- the same `secrets.X` reference appears multiple times in the same YAML scalar
- the same reference is found by two different passes but with slightly different `field_path` / `context_kind`
- the same script file is scanned from multiple workflow call sites

With Proposal v4, we now have a stronger, user-actionable identity:

- `(repo, scanned_ref, file_path, line_start)` → opens the exact line in GitHub.

---

## Goals
- Reduce duplicate rows in `snapshot.json` and `report.html`.
- Preserve information by merging (not dropping) context.
- Make merged rows more actionable: one row per underlying source location.

## Non-goals
- Not attempting semantic dedupe across different lines (e.g., same secret used on different lines).
- Not deduping across different `scanned_ref` values (different commits).

---

## Definitions
### Source link identity (primary dedupe key)
Define the canonical source identity for a finding:

- **Repo**: `repo_owner/repo_name`
- **Ref**: `repos[].scanned_ref` (commit SHA)
- **Path**:
  - if `file_kind == workflow_yaml` → `workflow_path`
  - else → `file_path`
- **Line**: `line_start`

Proposed key string:

- `source_key = <owner>/<repo>@<sha>:<path>#L<line_start>`

### Placeholder handling (Option 1)
If `path` contains:
- `__THIS_REPO__`
- `__BUILDER_CHECKOUT_DIR__`

Then the finding is **not linkable**.

For these:
- do **not** attempt source-link dedupe
- fall back to existing contextual dedupe key (current `findingKey` style)

---

## Proposed data model changes
### A) Add optional `source_key` to findings (recommended)
File: `internal/model/model.go`

Add:
- `SourceKey string 'json:"source_key,omitempty"'`

Rationale:
- Makes it easy for downstream tooling (diff/report) to group without recomputing.
- Keeps the snapshot self-contained.

### B) Add merged context fields (recommended)
When merging duplicates, we need to represent multiple contexts.

Option 1 (minimal): keep a single finding row and widen existing fields
- Replace single-valued fields with lists (bigger schema change).

Option 2 (recommended): add `Contexts []FindingContext` to a new `MergedFinding` type
- Introduce `MergedFindings []MergedFinding` while keeping legacy `Findings []Finding` for compatibility.

Given the repo already emits `Findings []Finding`, the least disruptive approach is:

- Keep `Findings` as-is for now.
- Implement dedupe+merge into a *single representative* `Finding` per `source_key`.
- Add additional merged context into a new optional field:
  - `MergedFrom []MergedFrom` (or `AltContexts []FindingContext`) to store the collapsed contexts.

If you want to avoid schema changes entirely, we can also do “report-only merge” (see below), but that does not reduce snapshot size.

---

## Deduplication + merge algorithm
### Step 1: Compute `source_key` (when possible)
For each finding:
1. Determine `ref = repo.scanned_ref`.
2. Determine `path` by `file_kind`:
   - workflow_yaml → `workflow_path`
   - otherwise → `file_path`
3. Validate:
   - `ref != ""`
   - `path != ""`
   - `line_start > 0`
   - `path` does not contain placeholder tokens
4. If valid, compute `source_key`.

### Step 2: Group findings
Group findings by:
- primary: `source_key` if present
- fallback: existing contextual key for un-linkable findings

### Step 3: Merge grouped findings
Within a group:
- Ensure the group is homogeneous for:
  - `ref_type` + `ref_name` (and usually `expression`)

If a group contains mixed `ref_type` or different `ref_name`, do **not** merge; keep separate groups by extending the grouping key:

- `source_key + "|" + ref_type + "." + ref_name`

Merged representative fields:
- Keep stable identifiers:
  - `repo_owner`, `repo_name`, `workflow_path`, `file_kind`, `file_path`, `ref_type`, `ref_name`, `expression`, `line_start`, `col_start`
- Merge context-ish fields:
  - `job_id`, `step_name`, `field_path`, `context_kind`, `action_uses`, `origin`

Recommended merge semantics:
- If multiple different values exist:
  - choose a “best” representative (e.g., prefer non-empty `job_id`, prefer AST-derived `context_kind` over `raw_scan`)
  - store the alternates in a merged context list (if schema allows)

### Step 4: Sorting / stability
To keep snapshots diff-friendly:
- sort merged findings by:
  - repo
  - path
  - line
  - ref_type
  - ref_name

---

## Where to implement
### Option A (recommended): merge during scan before writing snapshot
File: `cmd/secret-inventory/main.go`
- After scanning completes and `snapshot.Findings` is populated, run:
  - `snapshot.Findings = mergeFindings(snapshot)`

Pros:
- snapshot and report are both cleaner
- reduces snapshot size

Cons:
- introduces additional logic in scan pipeline

### Option B: merge only in report generation
File: `internal/report/report.go`
- Merge in-memory before rendering tables.

Pros:
- no snapshot schema changes

Cons:
- snapshot remains noisy
- downstream tooling still sees duplicates

---

## Report UX considerations
If we merge multiple contexts into one row, the report needs a way to show that.

Options:
- Add a “Count” column (number of merged contexts).
- Add a “Contexts” column summarizing job/step names (truncated).
- Add a details block under the row (static HTML) showing all contexts.

---

## Testing / verification
- Create a repo/workflow known to produce duplicates (AST + raw scan overlap).
- Verify:
  - merged findings count is lower
  - each merged finding still links to GitHub
  - contexts are preserved (no loss of job/step attribution)
- Ensure placeholder-path findings are not incorrectly merged by source link.

---

## Open questions
- Should merged findings be represented as:
  - (A) a single `Finding` with best-effort representative fields (minimal change)
  - (B) a new `MergedFinding` structure with explicit context lists (clearer but more schema/code changes)
 
  Decision: choose **(B)**.
 
- Should dedupe apply across different workflows if they point to the same repo file+line (scripts reused by many workflows)?
  - Default recommendation: **yes**, dedupe by source line regardless of which workflow led to scanning, but preserve originating workflow contexts in the merged context list.
 
  Decision: **yes**.
