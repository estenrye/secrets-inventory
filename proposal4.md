# Proposal v4: Line-level GitHub hyperlinks for findings

## Summary
Enhance the scanner and HTML report so each finding (secret/env/var/runtime_env) can include a **clickable hyperlink** that opens GitHub to the **exact file and line** where the reference occurs.

This proposal adds:

- **Accurate location metadata** in `snapshot.json` (line/column + a resolvable Git ref).
- **Stable GitHub permalinks** (prefer commit SHA) for long-term correctness.
- **Report UX updates** to show a “Source” link per finding.

The design supports both **github.com** and **GitHub Enterprise Server**.

---

## Goals
- Provide an actionable “jump to source” link for every finding.
- Avoid links that drift over time (prefer commit SHA permalinks).
- Keep the JSON snapshot diff-friendly and stable.
- Work with existing scanner modes:
  - workflow YAML scanning
  - script scanning (`runtime_env`)
  - local action scanning

## Non-goals (initial iteration)
- Not building an interactive UI; the report remains static HTML.
- Not implementing GitHub authentication in the browser (links rely on the user’s normal GitHub session).
- Not guaranteeing perfect line precision for every edge case; we’ll make it best-effort and explicit when only an approximate location is known.

---

## Background (current state)
- Findings currently include repo/workflow/job/step context and file path/kind.
- Scanner uses a mix of YAML AST traversal and raw-text scanning.
- The snapshot model has a `line_hint` concept in `proposal2.md`, but the implementation is currently not producing line+ref data intended for GitHub deep links.

---

## Requirements
### R1: Findings must include a stable Git reference
To form a correct URL, the report needs a specific ref:

- Preferred: `commit_sha` of the repo default branch HEAD at scan time.
- Acceptable fallback: `default_branch` (less stable over time).

### R2: Findings must include a source location
For each finding, store best-effort:

- `line_start` (1-indexed)
- `col_start` (optional, 1-indexed)
- `line_end` / `col_end` (optional)

### R3: Report must render a hyperlink
Each finding row should include a link like:

- `https://github.com/<owner>/<repo>/blob/<ref>/<path>#L<line>`

For a range:

- `...#L10-L14`

### R4: Enterprise support
If `config.github.base_url` is set to an enterprise API endpoint, generate the corresponding web URL host.

Example mapping:
- API base: `https://github.example.com/api/v3/`
- Web base: `https://github.example.com/`

---

## Proposed data model changes
File: `internal/model/model.go`

### 1) Add scan ref metadata per repo
Add to `model.Repo`:
- `ScannedRef` (string)
  - commit SHA used for permalink generation.

Rationale:
- Findings already include `RepoOwner`/`RepoName`; adding a per-repo scanned ref prevents repeating commit SHAs per finding.

### 2) Add location fields to `model.Finding`
Add to `model.Finding`:
- `LineStart` (int)
- `LineEnd` (int)
- `ColStart` (int)
- `ColEnd` (int)

If unknown, leave as `0`.

### 3) Clarify which path should be linked
Use existing fields:
- `WorkflowPath` is the workflow YAML path
- `FilePath` is the referenced file path for scripts/local actions
- `FileKind` indicates which path is relevant

Linking rules:
- If `FileKind == "workflow_yaml"`: link `WorkflowPath`
- Else: link `FilePath`

---

## Scanner changes (how to compute line numbers)
File: `internal/analyze/scanner.go`

### A) YAML AST-derived locations (preferred when available)
When a finding comes from a YAML scalar node, we can use `yaml.v3` node positions:
- `node.Line`
- `node.Column`

Approach:
- Extend the workflow walker callback to provide:
  - scalar node `Line`/`Column`
  - scalar string value
- When extracting `secrets.*` / `vars.*` / `env.*` from that scalar, set:
  - `LineStart=node.Line`
  - `ColStart=node.Column` (or offset inside scalar if computed)

Note:
- Precise column offsets inside the scalar require substring search. This can be added incrementally.

### B) Raw-text scan locations
For raw-text extraction, compute line number from the match index:

- During regex match, we have byte index `start`.
- Convert index -> line by counting `\n` up to `start`.

Optimization:
- Precompute newline offsets once per scanned file to support O(log n) line lookup.

### C) Script/local action locations
For script scanning:
- We already have match indices for `$NAME` / `${NAME}` occurrences.
- Apply the same index->line mapping to the script content.

### D) Edge cases
- Multi-line matches: set `LineEnd` when the match spans newlines.
- When only an approximate location is available:
  - set only `LineStart`
  - leave columns empty

---

## GitHub client changes (capture a permalink ref)
Files:
- `internal/githubclient/client.go`
- `cmd/secret-inventory/main.go`

### A) Resolve a commit SHA for each repo
At scan time, determine `HEAD` commit SHA for the default branch.

Options:
- Use `Repositories.Get` (already used to fetch repo metadata) and/or `Repositories.GetBranch` to obtain commit SHA.

Store:
- `repo.ScannedRef = <sha>`

### B) Ensure content links are correct for the scanned ref
For maximum correctness:
- When fetching workflow/script content, prefer fetching from the same ref used for `ScannedRef`.

Initial iteration (acceptable):
- Compute `ScannedRef` from default branch HEAD and keep current file fetch behavior.

---

## Report changes (HTML)
File: `internal/report/report.go`

### A) Add a “Source” column
Add a column that renders:
- Link text: `view`
- `href`: computed GitHub URL

### B) URL construction
Inputs:
- repo owner/name
- web base URL (`https://github.com` or enterprise web base)
- `ref`: `Repo.ScannedRef` if present else `Repo.DefaultBranch`
- file path
- line anchors

Anchor rules:
- If `LineStart > 0 && LineEnd > 0 && LineEnd != LineStart`: `#L<start>-L<end>`
- Else if `LineStart > 0`: `#L<start>`
- Else: no anchor

### C) Include base URL in snapshot or compute at report time
Options:

1) **Compute at report time** (recommended):
- Add a report option: `--github-web-base` or derive from config.

2) **Store in snapshot**:
- Add `Snapshot.GitHubWebBase`.

Recommended for now:
- Derive from config at scan time and store it in snapshot to keep `report.html` generation independent of having the config file later.

---

## Permissions impact
- Minimal contents read still works for raw references.
- To resolve `ScannedRef` (commit SHA), we need repository metadata endpoints already commonly available in the existing token scope.

No write permissions required.

---

## Implementation plan
1. **Model updates**
   - Add `ScannedRef` to repo metadata
   - Add line/col fields to findings

2. **Scanner location attribution**
   - Add line/col support for AST-derived findings
   - Add index->line mapping for raw-text findings
   - Add index->line mapping for script findings

3. **GitHub ref enrichment**
   - Resolve and store per-repo commit SHA

4. **Report UX**
   - Add “Source” column with computed GitHub hyperlinks

5. **Testing / verification**
   - Run scan on a repo with known `secrets.*` usage
   - Verify report links open correct file and line
   - Verify enterprise base URL mapping

---

## Open questions
- Should we always use commit SHA permalinks (best), or allow branch links for convenience?
  - Decision: always use **commit SHA permalinks** where possible.
  - Fallback (only if SHA cannot be determined): use default branch.

- For raw-text matches, do you want link precision to the exact line only, or do you also want the matched substring highlighted (requires additional UI; GitHub doesn’t support arbitrary substring anchors)?
  - Decision: **exact-line precision** (GitHub line anchors).
  - Note: GitHub does not support “substring anchors”; the best we can do in a static HTML report is anchor to the line (and optionally a line range).

- For local action placeholders like `__THIS_REPO__` / `__BUILDER_CHECKOUT_DIR__`, should we suppress hyperlink generation and display a “not linkable” indicator?
  - Explanation:
    - These strings are not real paths in the repository. They are placeholders inserted by upstream tooling/templates to represent:
      - `__THIS_REPO__`: “the repository root” conceptually, but not an actual folder.
      - `__BUILDER_CHECKOUT_DIR__`: a checkout directory path used during generation/build steps, not present in the repo contents API.
    - When our scanner tries to treat them like real repository files (e.g. `__THIS_REPO__/.github/workflows/...`), GitHub correctly returns `404`.
    - Because there is no real file to fetch, there is also no stable GitHub URL/line to link to.

  - Options:
    1. Suppress link generation for placeholder paths (recommended)
       - Report shows `Source: n/a` (or omits the link) and optionally a context label like `unresolvable_path_placeholder`.
       - This avoids broken links and keeps the report honest.

    2. Best-effort placeholder rewriting (conditional)
       - If a placeholder is used as a prefix (e.g. `__THIS_REPO__/path/to/file`), rewrite it to `path/to/file` and attempt to fetch/link.
       - This can work when the placeholder is essentially a templated alias for repo root.
       - Risk: can produce incorrect links if the placeholder is used in a more complex templated way.

    3. Always emit a link anyway (not recommended)
       - Would produce broken links (404) and degrade trust in the report.

  - Recommendation:
    - Implement option (1) by default.
    - Optionally add (2) behind a conservative rewrite rule set (only when the rewritten path is a normal relative path and exists).

  - Decision:
    - Use **option (1)**.
