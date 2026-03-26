# Proposal v2: Secret, Variable, and Environment Variable Usage Inventory Agent (GitHub Actions)

## Summary
Build a local CLI tool that scans GitHub repositories for GitHub Actions workflow files, extracts references to secrets/variables/environment variables, **and additionally scans repository scripts and local actions invoked by workflows** to identify environment-variable usage beyond the workflow YAML.

The tool tracks usage over time via snapshots, generates static JSON/HTML reports, and emits alerts when new references appear or usage violates default policies.

The system is designed for **least privilege**, ideally requiring only **read access to repository contents**.

---

## Goals
- Scan many repositories across:
  - multiple GitHub **organizations**
  - non-organization **user accounts**
  - optional explicit repo allowlists
- Identify and inventory usage across:
  - GitHub Actions workflow YAML under `.github/workflows/**`
  - repository scripts referenced by workflow steps (e.g. `./scripts/deploy.sh`)
  - local actions referenced via `uses: ./.github/actions/...`
- Track which workflows (and which jobs/steps/scripts) use:
  - **Secrets**: `${{ secrets.NAME }}` / `secrets.NAME`
  - **GitHub Variables**: `${{ vars.NAME }}` / `vars.NAME`
  - **Workflow expression env vars**: `${{ env.NAME }}` / `env.NAME`
  - **Runtime env vars in scripts**: `$NAME` / `${NAME}` (best-effort)
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
  - fetch referenced scripts and local action metadata from the repository

### Optional (future enrichment)
If additional permissions are available, the tool can optionally list the **names** of configured secrets/variables for:
- repository scope
- environment scope
- organization scope

This enrichment is not required to produce the core inventory, which is based on references in YAML and repository files.

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
  - script scanning options (extensions, max file size, include/exclude globs)

---

## Architecture
The CLI is organized into five subsystems:

1. **Repo/Workflow Harvester**
   - Enumerate repositories for each configured org/user (or use allowlists).
   - Discover workflow files under `.github/workflows/**`.
   - Fetch file contents (with caching via ETag / `If-None-Match` where possible).

2. **Workflow Analyzer**
   - Parse YAML into an AST when possible.
   - Extract references to `secrets.*`, `vars.*`, and `env.*`.
   - Capture usage context (repo/workflow/job/step/field-path).
   - Identify potential script and local-action entry points from steps.
   - Fallback to raw-text scanning when YAML parsing fails.

3. **Repository Script + Local Action Scanner**
   - Given entry points discovered from workflows:
     - fetch referenced script files from the repo
     - fetch local action metadata (`action.yml`/`action.yaml`) for `uses: ./...`
   - Scan these files for runtime environment-variable usage (`$NAME`, `${NAME}`) and additional references (`secrets.*`, `vars.*`, `env.*`) if present.
   - Attribute script usages back to the originating workflow job/step.

4. **Inventory + Change Detector**
   - Produce a normalized **snapshot**.
   - Optionally load the most recent previous snapshot.
   - Diff current vs previous to detect new/changed references.

5. **Report + Alerting**
   - Generate JSON + static HTML report.
   - Emit alerts for new references and policy violations.

---

## Workflow analysis approach
### Two-pass extraction (workflow YAML)
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

### Reference types (workflow YAML)
- **Secret reference**: `secrets.NAME`
- **GitHub variable reference**: `vars.NAME`
- **Expression environment reference**: `env.NAME`

---

## Repository script scanning (new in v2)
The goal of script scanning is to report environment-variable usage that is not visible in the workflow YAML, while still keeping attribution and false positives manageable.

### What counts as a “referenced script”
From each workflow step `run:` block, attempt to extract repository-local script entry points such as:

- `./path/to/script.sh`
- `bash ./path/to/script.sh`
- `sh ./path/to/script.sh`
- `python ./path/to/script.py`
- `node ./path/to/script.js`

This is intentionally conservative: only scripts that can be resolved to a repo path are scanned.

### Local actions (`uses: ./...`)
When a step uses a local action, e.g.:

- `uses: ./.github/actions/my-action`

Fetch and parse `action.yml` / `action.yaml`.

- If the local action is a **composite action**:
  - scan its `runs.steps[*].run` sections similarly (including referenced scripts)
- If the local action is a **node** action:
  - scan the referenced entrypoint file(s) (e.g. `runs.main`) if they are within the repo
- If the local action is a **docker** action:
  - treat script scanning as optional/limited (Dockerfile/entrypoint parsing can be added later)

### What the scanner looks for
In scanned scripts (and local-action `action.yml`):

- **Runtime env var usage** (best-effort):
  - `$NAME`
  - `${NAME}`
  - (optionally) `%NAME%` for Windows batch files if enabled

- **Expression references if present**:
  - `secrets.NAME`, `vars.NAME`, `env.NAME` (sometimes scripts contain copied `${{ }}` fragments)

### Attribution rules (important)
Script and local-action findings should always be attributed to an originating workflow location:

- `(repo, workflow_path, job_id, step_index/step_name)`
- plus the scanned file path (e.g. `scripts/deploy.sh`)

This keeps the report actionable: you can answer “which workflow step causes this usage?”

---

## Resolution: only report env vars that are meaningful
To avoid flooding reports with every `$PATH` or `$HOME`, treat runtime env var usages as “interesting” only when they can be connected to known workflow/secret/variable sources.

### Sources of “known” names
A runtime env var reference `$NAME` in a script should be reported when `NAME` is present in at least one of:

1. **Workflow-defined env**
   - workflow `env:`
   - job `env:`
   - step `env:`

2. **Workflow expression references**
   - the workflow references `secrets.NAME`, `vars.NAME`, or `env.NAME`

3. **Declared secrets/variables from GitHub API (optional enrichment)**
   - repo secrets (names)
   - repo variables (names)
   - org secrets/variables (names)
   - environment secrets/variables (names) if available

### Output classification
When reporting a runtime env var usage from scripts, include a classification:

- `origin: workflow_env` (NAME declared in env blocks)
- `origin: declared_secret` (NAME exists as a secret name)
- `origin: declared_variable` (NAME exists as a variable name)
- `origin: unknown` (optionally suppressed by default)

Default behavior should be conservative:
- report `workflow_env`, `declared_secret`, `declared_variable`
- suppress `unknown` unless `--include-unknown-env` is set

---

## Data model updates (snapshot)
In addition to the v1 model, add file-oriented locations.

### New/extended fields
- **Usage location** adds:
  - `file_path` (optional): repository path to scanned script or `action.yml`
  - `file_kind`: `workflow_yaml` | `script` | `action_yaml` | `action_entrypoint`
  - `line_hint` (optional, best-effort): line number range or match index

### New reference category (optional)
- `type: runtime_env` for `$NAME`/`${NAME}` occurrences.
  - These can still be grouped under “env usage”, but separating them helps reporting.

---

## Reporting updates
The HTML report should include additional drilldowns:

- **By file**: show env usage found in scripts and local actions
- **By workflow step**: show scripts invoked and which env vars they consume
- **By env var name**: show where `$NAME` is used across scripts/workflows

---

## Default policy set (inherits v1 + optional script-related checks)
All v1 policies remain.

### Additional script-related policy (optional)
**Policy H: Sensitive env vars used in scripts**
- **Rule**: If a script references `$NAME` where `NAME` is known to be a secret name (declared via API or referenced as `secrets.NAME`), flag.
- **Severity**: medium
- **Rationale**: Highlights where secrets likely flow into scripts, which is often where accidental logging occurs.

---

## Operational notes
- Script scanning should be bounded:
  - file size limits
  - extension allowlist
  - include/exclude globs
  - maximum number of files scanned per repo
- Cache fetched script contents via ETag where supported.
- Treat repository files as untrusted input; scanning must never execute code.
- Ensure logs never include expanded secrets (only reference names and usage locations).
