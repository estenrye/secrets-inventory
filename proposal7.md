# Proposal v7: Permission warnings for repositories that return 403 while reading workflows and scripts

## Summary
During the **core scan** (workflow YAML discovery/fetch + referenced script/local-action fetch), some repositories may return **HTTP 403 Forbidden** (or 404 in some permission-restricted configurations) when the tool attempts to read:

- `.github/workflows/*` workflow files (Contents API)
- referenced repository scripts (Contents API)
- local actions (e.g. `./.github/actions/.../action.yml`) and their referenced scripts

Today, these failures can be confusing because the scan may silently produce incomplete results (missing workflows, missing script-derived findings), and the user may interpret this as “no findings” rather than “insufficient permissions”.

This proposal adds **clear, deduplicated warnings** for these cases, emitted to stderr and included in `snapshot.json` and `report.html`.

---

## Goals
- Surface actionable warnings when a repository cannot be scanned due to **403** on workflow/script reads.
- Make warnings **concise** and avoid log spam:
  - dedupe per repository
  - group by explanation (console and report)
- Preserve the project’s **least-privilege** posture:
  - scanning remains “best effort”; warnings explain gaps
  - no new required permissions; just better diagnostics

## Non-goals
- No retries/backoff tuning changes (rate-limit/abuse detection is separate).
- No new auth flows or token validation.
- No attempt to automatically request/escalate permissions.

---

## Background
Per `permissions.md`, the core scanner requires **Repository Contents: Read** to list and fetch workflow YAML and any referenced scripts/local actions.

In practice, 403s can happen because:
- token does not have access to a given repo (fgPAT repo selection, org policy, SSO, etc.)
- GitHub App installation does not include a repo
- repo is private and the token only has public access
- enterprise policies restrict the Contents API

Today, a 403 at any of these points can lead to:
- no workflows discovered
- workflows discovered but content not fetched
- script/local action content not fetched (reducing `runtime_env` findings)

---

## User experience requirements

### R1: Emit warnings to stderr
Warnings should be printed to stderr in a format similar to existing deep-inspect permission warnings:

- Prefix with `warning:`
- Use **one warning per repo per category** (workflow read vs script/local-action read)
- By default, warnings are **collected and printed once at the end** of the scan
- When `--verbose` is set, warnings are also printed **immediately** as they occur

Example:

- `warning: repo OWNER/REPO cannot be scanned: GitHub returned 403 when reading workflow files (requires Contents: Read)`
- `warning: repo OWNER/REPO scanned partially: GitHub returned 403 when reading referenced scripts/local actions (requires Contents: Read)`

### R2: Persist warnings into `snapshot.json`
Add a new snapshot field for scan-stage permission warnings so the report can render them even if the console output is missed.

Recommendation:
- `snapshot.scan_warnings: []ScanWarning`

Where `ScanWarning` includes:
- `kind`: enum (`workflow_read_forbidden`, `script_read_forbidden`, `local_action_read_forbidden`, `repo_inaccessible`)
- `repo_owner`, `repo_name`
- `http_status`: int (e.g. 403)
- `operation`: string (e.g. `contents.list_workflows`, `contents.get_file`)
- `path`: optional (workflow/script/action path)
- `message`: a normalized human message (used for grouping)

Notes:
- This is separate from Proposal v6’s `deep_inspect_warnings`, which are about **declared inventory endpoints**.

### R3: Report banner grouping
In `report.html`, add a banner section near the top:

- Title: `Scan warnings` (or `Permission warnings`)
- Group by `message` (or by `kind` → message)
- Render repo list per group:
  - each repo wrapped in `<code>`
  - delimiter is plain text `, `

### R4: Avoid warning spam
Deduping rules:
- If multiple 403s occur in the same repo for multiple workflow files, emit **one** `workflow_read_forbidden` warning for that repo.
- If multiple scripts are blocked in the same repo, emit **one** `script_read_forbidden` warning for that repo.
- If both workflows and scripts are blocked, emit both warnings (still one each).

### R5: Distinguish “no workflows” from “cannot read workflows”
Today, repos without workflows commonly return 404 for `.github/workflows`.

Rules:
- `404 Not Found` for `.github/workflows` should remain treated as “no workflows” (not a warning).
- `403 Forbidden` should produce a warning because it indicates the tool is blocked.
- Some GitHub configurations may return 404 for inaccessible private repos. If we can reliably detect “permission-like 404” (e.g., via prior repo metadata listing, or error payload), we should treat it similarly to 403.

---

## Technical design

### A) Detection points
We should detect 403s at the **authoritative fetch points**:

1. **Workflow discovery**
   - listing `.github/workflows` via Contents API

2. **Workflow content fetch**
   - fetching each `.yml/.yaml` via Contents API

3. **Script/local action fetch**
   - fetching referenced `./scripts/...` (from `run:`) and local action metadata (`action.yml`) via Contents API

These are likely implemented in the GitHub client wrapper (`internal/githubclient/client.go`) and called from `cmd/secret-inventory/main.go` scanning pipeline.

Recommendation:
- Introduce a small helper that classifies “permission-like failures”:
  - `isForbiddenOrPermissionNotFound(err) (status int, ok bool)`
- Ensure this helper works for both go-github errors and raw REST errors.

### B) Warning aggregation
Implement a centralized warning collector so warnings aren’t emitted from deep internal loops.

Option 1 (recommended): collect at scan orchestration layer
- In `runScan`, when an operation returns a permission error:
  - add a warning to a `map[repoKey]map[kind]ScanWarning` (dedupe)
  - continue scanning other repos
- After scanning:
  - append warnings to `snapshot.scan_warnings`
  - print grouped warnings to stderr

Option 2: collect inside githubclient
- Harder to attribute to repo operations cleanly.
- Risk of duplicative warnings across call sites.

### C) Snapshot schema
File: `internal/model/model.go`

Add:
- `ScanWarnings []ScanWarning 'json:"scan_warnings,omitempty"'`

And:
- `type ScanWarning struct { Kind, RepoOwner, RepoName, HTTPStatus, Operation, Path, Message ... }`

Note:
- Keep fields optional and omit empty values to stay diff-friendly.

### D) Report rendering
File: `internal/report/report.go`

Add a banner section similar to the deep-inspection warnings banner:
- Group warnings by `Message`.
- Render repo lists with the established formatting:
  - each repo name individually `<code>`
  - delimiter `, ` not code-formatted.

---

## Warning message catalog (normalized)
Suggested normalized `Message` values (these are grouping keys):

1. `Token lacks permission to read GitHub Actions workflow files for these repositories (requires Contents: Read).`
2. `Token lacks permission to read referenced scripts/local actions for these repositories (requires Contents: Read).`

Notes:
- Prefer “token lacks permission” language because it covers fgPAT/GitHub App/SSO cases.
- Keep messages short and actionable.

---

## Edge cases and caveats
- **Inaccessible private repos** may present as 404 depending on GitHub configuration. This proposal intentionally does **not** treat 404 as a permission warning; 404 is handled as “not found” (no workflows / missing path).
- **Archived repos**: still warn if 403 occurs; archived does not imply inaccessible.
- **Rate limits** (403 with rate-limit headers) should not be conflated with permission. Ideally, detect rate-limit vs permission by inspecting response headers/messages and classify separately (future follow-up).

---

## Implementation plan
1. **Model**: add `ScanWarnings` / `ScanWarning` types to snapshot.
2. **Classifier**: add a helper for permission-like 403 errors (core scan path).
3. **Aggregation**: collect + dedupe warnings per repo/kind in the scan pipeline.
4. **Console UX**: print grouped warnings once at end of scan; when `--verbose` is set, also print immediately per repo as errors occur.
5. **Report UX**: render a grouped warnings banner using the established repo list formatting.
6. **Verification**:
   - add a small check to `tools/verify_links.py` ensuring `scan_warnings` appear when expected
   - run a scan against a config that includes at least one repo that triggers 403 on contents.

---

## Open questions
1. Should the tool print warnings **immediately** as they occur, or only once at the end?
   - Decision: by default, collect and print once at end; when `--verbose` is set, also print immediately.

2. Should we treat permission-like 404s the same as 403?
   - Recommendation: no. Keep 404 semantics distinct from 403. A 404 for `.github/workflows` should generally mean “no workflows”, and a 404 for a referenced script/action path should generally mean “path not present”.
