# Proposal v6: Optional deep inspection of declared Secrets/Variables and unused inventory

## Summary
Add an **optional CLI flag** that enables deeper GitHub inspection to:

- List **declared GitHub Actions secrets** (names only) across scopes:
  - organization
  - repository
  - environment
- List **declared GitHub Actions variables** (names only) across scopes:
  - organization
  - repository
  - environment
- Cross-reference declared names against detected usage (`secret` + `var` findings) to determine:
  - which declared items are **used**
  - which declared items are **unused**
- Enhance the HTML report with:
  - a **Manage** link per secret/variable finding that opens the GitHub UI where the item can be edited
  - a section/table listing **unused secrets and variables**, broken down by scope, with a **Manage** link per secret/variable finding that opens the GitHub UI where the item can be edited

This feature is designed to preserve the project’s **least-privilege** posture: the default scan remains content-only; deep inspection is opt-in and requires additional read permissions.

---

## Goals
- Provide an accurate inventory of **declared** secrets/variables (names only) and their **scope**.
- Identify and report **unused** secrets/variables.
- Make findings more actionable by providing a **direct GitHub UI link** to manage the referenced secret/variable.
- Keep the snapshot diff-friendly and stable.

## Non-goals
- Never fetch or display secret values.
- Not implementing write/update operations (links go to GitHub UI; edits are performed by the user).
- Not attempting to resolve every reference to a specific scope when multiple scopes may define the same name (we will present “candidates” and/or “unknown” where resolution is ambiguous).

---

## Background
Current scanning detects references to:
- `secrets.NAME` (findings with `ref_type == "secret"`)
- `vars.NAME` (findings with `ref_type == "var"`)

The tool already supports optional enrichment hooks (documented in `permissions.md`) for listing repo/org secrets/variables. Proposal v6 formalizes and extends that enrichment to:

- include **environment scope**
- compute **used vs unused**
- enhance the report with **Manage** links

This proposal leverages Proposal v5’s `merged_findings` to avoid double-counting “usage” when the same underlying occurrence is detected multiple ways.

---

## CLI UX
### New flag
Add an opt-in flag to `scan`:

- `--deep-inspect`

Behavior:
- When enabled, the CLI performs additional API calls to list declared secrets/variables.
- When disabled, the CLI behaves exactly as today.

Optional follow-ups (future):
- `--deep-inspect-org` / `--deep-inspect-env` toggles if you want finer control.

---

## Permissions impact
This feature requires additional **read-only** permissions, depending on scopes enabled.

Referencing `permissions.md`:

- Repository secrets (names): read
- Repository variables (names): read
- Organization secrets (names): read
- Organization variables (names): read
- Environment secrets/variables: read (requires environment APIs)

The tool should degrade gracefully:
- If a token lacks permissions for some scope(s), emit a warning and continue.

---

## GitHub API requirements (names only)
### Repository scope
- **Variables**
  - `GET /repos/{owner}/{repo}/actions/variables`
- **Secrets**
  - `GET /repos/{owner}/{repo}/actions/secrets`

### Organization scope
- **Variables**
  - `GET /orgs/{org}/actions/variables`
- **Secrets**
  - `GET /orgs/{org}/actions/secrets`

### Environment scope
Two-step discovery:

1) List environments in a repository:
- `GET /repos/{owner}/{repo}/environments`

2) For each environment name:
- **Secrets**
  - `GET /repos/{owner}/{repo}/environments/{environment_name}/secrets`
- **Variables**
  - `GET /repos/{owner}/{repo}/environments/{environment_name}/variables`

Notes:
- Environment enumeration can be expensive; the implementation should support pagination + rate limiting.
- If environment APIs are not available (or blocked), skip environment scope.

---

## Proposed snapshot schema changes
File: `internal/model/model.go`

Add a new section to `snapshot.json` to record declared items.

### A) Declared items
Add two collections:

- `declared_secrets`: list of declared secret names + scope
- `declared_variables`: list of declared variable names + scope

Proposed records (conceptual):

- `DeclaredItem`
  - `kind`: `secret` | `var`
  - `name`
  - `scope_kind`: `org` | `repo` | `environment`
  - `org` (if scope is org)
  - `repo_owner`/`repo_name` (if scope is repo or environment)
  - `environment` (if scope is environment)
  - `manage_url` (GitHub UI URL)
  - `used` (boolean)
  - `used_by`: optional summary (e.g., count of merged findings or list of refs)

Rationale:
- Keep scan output self-contained.
- Enable future diffing/policy checks around unused items.

### B) Finding enrichment (optional)
For findings where `ref_type` is `secret` or `var`, add optional fields:

- `declared_candidates` (list)
  - if the same name exists at multiple scopes, include all matching declared items

Or the minimal alternative:
- `declared_scope_hint`: `repo` | `org` | `environment` | `unknown`

Recommendation:
- Start with snapshot-level declared lists + “used” computation.
- Add per-finding candidate resolution only if needed for UX.

---

## Cross-referencing logic (used vs unused)
### Canonical “used” set
Compute used names from **merged findings** to avoid duplicate counting:

- `used_secrets = set of merged_findings where ref_type == "secret" -> ref_name`
- `used_vars = set of merged_findings where ref_type == "var" -> ref_name`

### Mapping declared → used
For each declared item:
- If its `name` is in the corresponding used set, mark `used = true`.

Important caveat:
- The same name can exist in multiple scopes. Without additional context (e.g., workflow environment selection and GitHub resolution rules), we may not know which scope is “the one actually used”.

Recommendation:
- Default behavior:
  - mark **all declared items with matching names** as “used”
  - optionally add an ambiguity marker for names that exist in multiple scopes

Optional refinement (future):
- Attempt to resolve environment scope usage when:
  - the workflow/job explicitly sets `environment:` and the name exists only within that environment.

---

## GitHub UI Manage links
Add a “Manage” link per secret/variable finding and also store `manage_url` on declared items.

### Repository
- Repo secrets UI:
  - `https://github.com/{owner}/{repo}/settings/secrets/actions`
- Repo variables UI:
  - `https://github.com/{owner}/{repo}/settings/variables/actions`

### Organization
- Org secrets UI:
  - `https://github.com/organizations/{org}/settings/secrets/actions`
- Org variables UI:
  - `https://github.com/organizations/{org}/settings/variables/actions`

### Environment
- Repo environment UI:
  - `https://github.com/{owner}/{repo}/settings/environments/{environment_name}`

Notes:
- GitHub’s environment page contains both secrets and variables; the link can target the environment page.
- For GHES, the base host should use the same `github_web_base` logic introduced in Proposal v4.

---

## Report changes (HTML)
File: `internal/report/report.go`

### A) Findings table: add “Manage” column
For findings where:
- `ref_type == "secret"` or `ref_type == "var"`

Add:
- `Manage` column containing a link to the best available UI page.

If scope cannot be determined:
- link to the repo-level page by default (least confusing starting point)
- optionally show a tooltip or text like `scope: unknown`.

### B) Add unused items section
Add report sections:

- `Unused secrets`
- `Unused variables`

Each section should be broken down by scope:
- Org
- Repo
- Environment

Recommended columns:
- `Scope`
- `Owner/Repo`
- `Environment` (if applicable)
- `Name`
- `Manage` link

### C) Summary metrics
Add counts:
- total declared secrets / variables
- unused secrets / variables

---

## Implementation plan
1. **CLI flag and config plumbing**
   - add `--deep-inspect`

2. **GitHub client support**
   - add helpers to list:
     - repo secrets/vars
     - org secrets/vars
     - repo environments + env secrets/vars

3. **Snapshot model updates**
   - add declared lists + used flags

4. **Cross-reference step**
   - compute used sets from `merged_findings`
   - mark declared items as used/unused

5. **Report updates**
   - add Manage column for findings
   - add unused items section

6. **Verification**
   - extend `tools/verify_links.py` to check:
     - presence of declared lists when deep-inspect is enabled
     - unused sections in report

---

## Open questions

Decisions:

- Environment scope **should be enabled by default** when `--deep-inspect` is set.
- The tool should **attempt to resolve the exact scope** used by a finding (org vs repo vs environment), and **fail gracefully** to listing scope candidates when resolution is not possible.
- Declared-name listing **should be supported for user targets** (non-org) where org endpoints may not apply.
