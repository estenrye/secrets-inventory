# Proposal v3: Reporting split into Secrets / Env / Vars tables

## Summary
This proposal updates the reporting design from `proposal2.md` so the HTML report output can present findings in **three separate tables**:

- a table for **secrets** findings
- a table for **env** findings
- a table for **vars** findings

The intent is to make the report easier to scan and to align presentation with the core reference categories described in `spec.md` and `proposal2.md`.

---

## Background (current state)
`proposal2.md` describes a report that includes:

- Overview metrics
- Drilldowns
- A consolidated “Findings” list

The current CLI implementation produces findings with `ref_type` values including:

- `secret`
- `var`
- `env`
- `runtime_env` (script/runtime `$NAME` usage)

This proposal focuses specifically on splitting the **report output** into separate tables for `secret`, `env`, and `var`.

---

## Requirements
### R1: Three top-level findings tables
In the HTML report, show three separate tables:

1. **Secrets**
   - Includes findings where `ref_type == "secret"`

2. **Env**
   - Includes findings where `ref_type == "env"`

3. **Vars**
   - Includes findings where `ref_type == "var"`

### R2: Stable columns across tables
To keep the report easy to compare across tables, the column set should be consistent.

Recommended columns (same as the current consolidated table):

- `Repo`
- `Workflow`
- `Job`
- `Step`
- `File` (kind + optional file path)
- `Ref` (type.name)
- `Context`

### R3: Define how `runtime_env` is handled
`proposal2.md` introduced `runtime_env` findings. These do not belong naturally in the `env` table if the requirement is strictly “env (expression)”.

Options:

- **Option A (recommended): keep `runtime_env` separate**
  - Keep the report split into three required tables (secrets/env/vars)
  - Add a **fourth optional table** “Runtime env”
  - This preserves the meaning of `env` as `${{ env.NAME }}` / `env.NAME` expression references.

- **Option B: fold `runtime_env` into the Env table**
  - The Env table becomes “Env (expression + runtime)”.
  - This makes Env much larger and less precise, but meets a strict “three tables only” constraint.

This proposal recommends **Option A** for clarity, but supports either depending on desired UX.

---

## Proposed report structure changes
### 1) Summary section updates
In addition to existing totals, include per-type counts displayed prominently:

- Secrets count (`ref_type == secret`)
- Vars count (`ref_type == var`)
- Env expr count (`ref_type == env`)
- (Optional) Runtime env count (`ref_type == runtime_env`)

This is already aligned with the metrics described in `proposal.md` and helps users navigate to the right table.

### 2) Findings section split
Replace the single “Findings” table with sections:

- `## Secret findings`
- `## Env findings`
- `## Var findings`

Each section renders only its corresponding findings.

### 3) Sorting
Within each table, sort findings for readability:

- Primary: `repo_owner/repo_name`
- Secondary: `workflow_path`
- Then: `job_id`, `step_index`, `step_name`
- Then: `file_kind`, `file_path`
- Then: `ref_name`

This makes it easier to compare changes and quickly locate where a reference occurs.

### 4) Filtering and navigation (optional)
Add lightweight navigation links at the top:

- `Secrets` (anchor link)
- `Env`
- `Vars`
- (Optional) `Runtime env`

This keeps the report usable even for large scans.

---

## Data model impact
No snapshot schema changes are required.

The split is purely a **presentation concern** driven by `ref_type`.

However, the report generator should treat `ref_type` values as an enum and group accordingly:

- `secret` group
- `env` group
- `var` group
- `runtime_env` group (optional)

---

## Implementation plan (Go CLI)
### Report generator changes
In the HTML report generator:

- Partition findings into slices:
  - `secretFindings`
  - `envFindings`
  - `varFindings`
  - `runtimeEnvFindings`

- Render each slice with the same table template.

A simple approach is to implement a reusable template block for a table given a title + slice.

### Testing
- Generate a snapshot with at least one finding in each category.
- Verify:
  - each finding appears exactly once in the correct table
  - counts match summary metrics

---

## Open question
Decision: `runtime_env` findings will appear as a **separate fourth table** ("Runtime env"), not merged into the `Env` table.
