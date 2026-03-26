# Proposal: Secret & Environment Variable Usage Inventory Agent (GitHub Actions)

## Summary
Build a local CLI tool that scans GitHub repositories for GitHub Actions workflow files, extracts references to secrets and variables, tracks usage over time via snapshots, generates static JSON/HTML reports, and emits alerts when new references appear or when usage violates default policies.

The system is designed for **least privilege**, ideally requiring only **read access to repository contents**.

---

## Goals
- Scan many repositories across:
  - multiple GitHub **organizations**
  - non-organization **user accounts**
  - optional explicit repo allowlists
- Identify and inventory in workflow files:
  - **Secrets** references: `${{ secrets.NAME }}` / `secrets.NAME`
  - **Environment expression** references: `${{ env.NAME }}` / `env.NAME`
  - **GitHub Variables** references: `${{ vars.NAME }}` / `vars.NAME`
- Track which workflows (and which steps) reference which secrets/vars/env values.
- Produce a **static**:
  - machine-readable **JSON** snapshot
  - human-readable **HTML** report
- Alert on:
  - newly introduced secret/variable references
  - “unexpected” uses based on a default policy set

---

## Non-Goals (MVP)
- No attempt to retrieve or display **secret values**.
- No runtime analysis of workflow execution.
- No requirement for write operations (e.g., opening PRs, filing issues) in the default configuration.

---

## Permissions model (least privilege)
### Minimum required
- **Read access to repository contents**
  - list `.github/workflows/`
  - fetch workflow YAML file content

### Optional (future enrichment)
If additional permissions are available, the tool can optionally list the **names** of configured secrets/variables for:
- repository scope
- environment scope
- organization scope

This enrichment is not required to produce the core inventory, which is based on references in YAML.

---

## CLI runtime model
### Primary command
- `secret-inventory scan --config config.yml --out out/ --format json,html`

### Optional commands (can be submodes or flags)
- `secret-inventory diff --previous <snapshot.json> --current <snapshot.json>`
- `secret-inventory report --input <snapshot.json> --format html`

### Inputs
- `config.yml` defines:
  - targets (orgs/users/repos)
  - output directory
  - reporting format options
  - policy enablement and overrides
  - alert destinations

---

## Architecture
The CLI is organized into four subsystems:

1. **Repo/Workflow Harvester**
   - Enumerate repositories for each configured org/user (or use allowlists).
   - Discover workflow files under `.github/workflows/**`.
   - Fetch file contents (with caching via ETag / `If-None-Match` where possible).

2. **Workflow Analyzer**
   - Parse YAML into an AST when possible.
   - Extract references to `secrets.*`, `vars.*`, and `env.*`.
   - Capture usage context (repo/workflow/job/step/field-path).
   - Fallback to raw-text scanning when YAML parsing fails.

3. **Inventory + Change Detector**
   - Produce a normalized **snapshot**.
   - Optionally load the most recent previous snapshot.
   - Diff current vs previous to detect new/changed references.

4. **Report + Alerting**
   - Generate JSON + static HTML report.
   - Emit alerts for new references and policy violations.

---

## Workflow analysis approach
### Two-pass extraction
1. **AST-aware pass (preferred)**
   - Walk common workflow fields and scan scalar strings:
     - workflow/job/step `env:` blocks
     - `steps[*].run`
     - `steps[*].with` inputs
     - `steps[*].uses`
     - reusable workflow calls and `secrets:` mappings (where present)

2. **Raw-text fallback pass**
   - Regex scan the YAML text to catch:
     - unusual/edge formatting
     - partially invalid YAML
     - references outside the walked fields

### Reference types
- **Secret reference**
  - `secrets.NAME`
- **GitHub variable reference**
  - `vars.NAME`
- **Expression environment reference**
  - `env.NAME`

### Optional shell env usage (best-effort)
- Detect `$NAME` / `${NAME}` inside `run:` scripts.
- Only attribute these to a defined env var if resolvable from:
  - workflow `env:`
  - job `env:`
  - step `env:`

---

## Data model (snapshot)
Snapshots should be stable and diff-friendly.

### Core records
- **Repository**
  - `owner`, `name`, `default_branch`, `visibility`, `archived`

- **WorkflowDocument**
  - `repo`
  - `path`
  - `ref` / `commit_sha`
  - `parsed_ok`, `parse_errors` (if any)

- **Reference**
  - `type`: `secret` | `var` | `env`
  - `name`: `MY_SECRET`
  - `expression`: exact matched string (e.g., `secrets.MY_SECRET`)

- **Usage location**
  - `workflow_path`
  - `job_id` (when known)
  - `step_index` and/or `step_name` (when known)
  - `field_path` (best-effort; e.g. `jobs.build.steps[2].env.MY_VAR`)
  - `context_kind`: `env_block` | `run_script` | `with_input` | `uses_action` | `reusable_workflow_call`
  - `action_uses`: if applicable (e.g. `aws-actions/configure-aws-credentials@v4`)

### Usage edges
Normalize inventory as edges:
- `(repo, workflow_path, ref_type, ref_name) -> [locations...]`

This makes it straightforward to:
- query “where is secret X used?”
- build reports
- compute diffs over time

---

## Reporting
### JSON
- Write a single snapshot file (and optionally a `snapshots/` history folder).
- Include:
  - scan metadata (timestamp, targets, repo/workflow counts)
  - normalized usage edges
  - policy evaluation results (if enabled)

### HTML
Generate a static report that reads from the JSON snapshot and provides:
- overview metrics
  - repos scanned
  - workflows found
  - unique secrets/vars/env referenced
- drilldowns
  - by repo
  - by workflow
  - by reference name
- policy violations list with explanations

---

## Alerting
### Event types
- `reference.added`
- `reference.removed`
- `reference.moved` (same reference but different location)
- `policy.violation`

### Destinations
- Start with:
  - stdout summary
  - optional webhook (e.g. Slack)
  - optional email

---

## Default policy set
This policy set is designed to be useful with low false positives and requires only workflow YAML access.

### Policy A: New reference detection
- **Rule**: Alert on any new `secrets.*` or `vars.*` reference compared to the previous snapshot.
- **Severity**: medium

### Policy B: Secrets in PR-triggered workflows
- **Rule**: If `on:` includes PR-related triggers, flag any `secrets.*` references.
  - `pull_request_target` + secrets => **high**
  - other PR triggers + secrets => **medium**
- **Severity**: medium/high

### Policy C: Secrets in `run:` scripts
- **Rule**: Flag `secrets.*` referenced directly inside `run:` blocks.
- **Severity**: medium
- **Rationale**: Higher risk of accidental logging/exfiltration.

### Policy D: Secrets passed to unpinned or untrusted actions
Subrules:

1) **Unpinned third-party action**
- **Rule**: If a step `uses:` an action not pinned to a full commit SHA, and secrets are passed via `with:` or `env:`, flag.
- **Severity**: high for third-party owners; medium for GitHub-owned/org-owned (configurable).

2) **Trusted actions allowlist**
- **Rule**: If secrets are passed to an action not in `trusted_actions`, flag.
- **Severity**: medium

### Policy E: Over-broad secret injection into environment
- **Rule**: Flag `secrets.*` used in top-level `env:` or job-level `env:` blocks (unless allowed).
- **Severity**: medium

### Policy F: “Vars look like secrets” heuristic (optional, disabled by default)
- **Rule**: Flag `${{ vars.NAME }}` where `NAME` matches patterns like `TOKEN|SECRET|PASSWORD|PASS|KEY|CREDENTIAL`.
- **Severity**: low

### Policy G: Missing/invalid reference name (optional)
- **Rule**: If the tool can list secret/var names via API, flag references to unknown names.
- **Severity**: low

---

## Configuration and overrides
Policies should be configurable with sensible defaults:
- enable/disable per policy
- allowlists by:
  - repo (`owner/name`)
  - workflow path glob (`.github/workflows/deploy*.yml`)
  - job id
  - step name match
- trusted action owners and trusted action list

---

## Operational notes
- Use concurrency per repo with rate limiting.
- Cache workflow files with ETag to reduce API usage.
- Treat workflow YAML as untrusted input.
- Ensure logs never include expanded secrets (only reference names and locations).
