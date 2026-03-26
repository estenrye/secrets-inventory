# ai-output-2: Decisions and actions taken

## Decisions you made
- **Scope**
  - Target multiple GitHub **organizations** and non-organization **user accounts**.
- **Runtime**
  - Must be executable as a **local CLI**.
- **Output format (current milestone)**
  - A **static JSON + HTML report** is sufficient for now.
- **Policies**
  - Requested a **default policy set** to define “unexpected” secret usage.
- **Design enhancement**
  - Required scanning of **repository scripts referenced by workflows** (and local actions) to capture runtime environment variable usage beyond YAML.
- **Versioning standards**
  - Prefer using **latest dependency versions**.
  - Standardize the project on **Go 1.26.1**.

---

## Actions taken (what was implemented/added)

### Design documents created/updated
- **Created `proposal.md`**
  - Consolidated the initial agent/CLI design into a single proposal document.
- **Created `proposal2.md`**
  - Enhanced the design to include:
    - scanning repo-local scripts referenced by `run:`
    - scanning local actions referenced via `uses: ./...`
    - attributing script/local-action env usage back to the originating workflow step
    - filtering runtime env findings to “meaningful” variables unless explicitly enabled
- **Created `references.md`**
  - Documented:
    - **AST** (Abstract Syntax Tree) meaning and its use for YAML parsing/tree-walking
    - **ETag** meaning and how it enables conditional requests (`If-None-Match` / `304 Not Modified`)
- **Created `permissions.md`**
  - Elaborated least-privilege permissions and mapped permissions to the tool’s stages and API calls.

### Go CLI scaffold generated from `proposal2.md`
Implemented a runnable Go CLI with a `scan` command that:

- Resolves scan targets from config:
  - orgs/users/repo allowlist
- Discovers workflows by listing `.github/workflows/*.yml|*.yaml`
- Fetches workflow files using the Contents API
- Extracts references from workflow YAML:
  - `secrets.NAME`, `vars.NAME`, `env.NAME`
- Detects repo-local scripts in `run:` blocks and scans them for:
  - runtime env usage `$NAME` / `${NAME}` (reported as `runtime_env`, filtered by default)
- Detects and scans local actions (`uses: ./...`):
  - fetches `action.yml|action.yaml`
  - for composite actions, scans `runs.steps[*].run` and follows script references
  - for node actions, follows `runs.main` entrypoint (extension allowlist)
- Produces outputs:
  - `snapshot.json`
  - `report.html`
- Implements basic ETag caching:
  - reads/writes `out/.cache/etags.json`
  - sends `If-None-Match` to avoid refetching unchanged files

### Files added for the Go CLI
- `go.mod`
- `cmd/secret-inventory/main.go`
- `internal/config/config.go`
- `internal/model/model.go`
- `internal/githubclient/client.go`
- `internal/githubclient/etag.go`
- `internal/analyze/scanner.go`
- `internal/report/report.go`
- `config.example.yml`

### Build verification
- Ran `go mod tidy` and `go build ./...` successfully.

---

## Version upgrades performed
- Updated `go.mod` to:
  - `go 1.26.1`
  - `toolchain go1.26.1`
- Upgraded `go-github`:
  - from `github.com/google/go-github/v66` to `github.com/google/go-github/v84`
- Updated code imports accordingly.

---

## Notes on current state
- The CLI is currently focused on **scanning + reporting**.
- Policy evaluation, diffing snapshots, and alert delivery are not yet implemented in code (design is documented in proposal files).
