# Permissions

This project is designed to work with **least privilege**. The core workflow inventory can be built using only **read access to repository contents**. Any permissions beyond that are optional and only used for enrichment.

This document describes:

- The **minimum fine-grained permissions** required
- Optional permissions for better inventory accuracy/metadata
- **Where** each permission is used (which GitHub API endpoints/features)

---

## Authentication modes
### Fine-grained Personal Access Token (fgPAT)
Best for a local CLI MVP.

- Can be restricted to:
  - specific repositories, or
  - all repositories in specific organizations the user has access to

### GitHub App (recommended long-term)
Best for scaling across many orgs with consistent permission boundaries.

- Install the app into orgs and grant it access to selected repos.
- The CLI can exchange for an installation token.

This project’s permission needs map cleanly to GitHub App permissions.

---

## Minimum required permissions (core scanning)
These permissions support:

- enumerating repositories (if scanning an org/user broadly)
- reading `.github/workflows/*` files
- generating references inventory from YAML

### 1) Repository Contents: Read
**Why it’s needed**
- To list and fetch GitHub Actions workflow files stored in the repository.

**Where it’s used**
- Listing workflow directory entries:
  - `GET /repos/{owner}/{repo}/contents/.github/workflows`
- Fetching workflow file contents:
  - `GET /repos/{owner}/{repo}/contents/{path}`
- Optional optimization for large repos (single-file fetch by git object):
  - `GET /repos/{owner}/{repo}/git/trees/{tree_sha}?recursive=1`
  - `GET /repos/{owner}/{repo}/git/blobs/{blob_sha}`

**How it’s used in the tool**
- Repo/Workflow Harvester calls the Contents API (or Git trees/blobs) to:
  - discover `.yml`/`.yaml` files under `.github/workflows/`
  - retrieve each file’s YAML text
- The harvester should use conditional requests when available:
  - read `ETag` from responses
  - send `If-None-Match` on subsequent scans

### 2) Repository Metadata / Listing repos (read)
This depends on whether you scan via **repo allowlist** or **org/user enumeration**.

#### If you scan an explicit repo allowlist
- You can avoid broad listing permissions entirely.
- The CLI simply attempts to read the workflow directory for each `owner/repo`.

#### If you enumerate repos under org/user
You need enough read access to list repositories.

**Where it’s used**
- List org repos:
  - `GET /orgs/{org}/repos`
- List user repos:
  - `GET /users/{username}/repos` (public)
  - `GET /user/repos` (for the authenticated user, includes private depending on token grants)

**How it’s used in the tool**
- Target discovery phase produces a list of repos to scan.
- The tool should support both:
  - discovery mode (org/user enumeration)
  - allowlist mode (no enumeration)

---

## Optional permissions (enrichment)
The inventory can be built entirely from YAML references. The following permissions only improve context and validation.

### A) Actions: Read (workflow metadata)
**Why it’s useful**
- Read-only access to GitHub Actions metadata can enable additional features such as:
  - enumerating workflow files known to GitHub (as opposed to filesystem discovery)
  - correlating references with workflow run data (future)

**Where it’s used**
- List workflows in a repo:
  - `GET /repos/{owner}/{repo}/actions/workflows`
- Get a workflow by id:
  - `GET /repos/{owner}/{repo}/actions/workflows/{workflow_id}`

**How it’s used in the tool**
- Alternative workflow discovery method (optional).
- Not required for the MVP inventory, which can be built from contents alone.

### B) Repository Variables: Read (names only)
**Why it’s useful**
- To validate `${{ vars.NAME }}` references and optionally flag unknown names.

**Where it’s used**
- List repo variables:
  - `GET /repos/{owner}/{repo}/actions/variables`

**How it’s used in the tool**
- Enrichment pass after YAML reference extraction:
  - mark `vars.NAME` references as “exists in repo scope” if present
  - otherwise “unknown (not found)” (with the caveat that org/environment scope might still exist)

### C) Repository Secrets: Read (names only)
**Why it’s useful**
- To validate `${{ secrets.NAME }}` references and optionally flag unknown names.

**Where it’s used**
- List repo secrets:
  - `GET /repos/{owner}/{repo}/actions/secrets`

**How it’s used in the tool**
- Enrichment pass similar to variables.
- Note: GitHub APIs never expose secret *values*, only metadata such as names.

### D) Organization-level variables/secrets: Read (names only)
**Why it’s useful**
- To determine whether a reference comes from org scope vs repo scope.

**Where it’s used**
- Org variables:
  - `GET /orgs/{org}/actions/variables`
- Org secrets:
  - `GET /orgs/{org}/actions/secrets`

**How it’s used in the tool**
- Enrichment pass for org targets:
  - annotate references with “org-scoped candidate”

### E) Environment-level secrets/variables (names only)
**Why it’s useful**
- GitHub Actions environments can have their own secrets/variables. Referenced names may resolve via environment scope.

**Where it’s used**
- Environment secrets/variables APIs exist but require additional permissions and environment context.

**How it’s used in the tool**
- Optional enrichment only.
- The analyzer can still detect references in YAML without this.

---

## How the CLI uses permissions (by stage)
### Stage 1: Target discovery
- **Org/User enumeration** (optional)
  - requires repo listing read access
- **Repo allowlist mode**
  - does not require listing permissions; only per-repo content reads

### Stage 2: Workflow discovery
- Primary: Contents read (list `.github/workflows/`)
- Optional: Actions read (list workflows via Actions API)

### Stage 3: Content fetch + caching
- Contents read to fetch each workflow YAML
- Optional optimization:
  - use `ETag`/`If-None-Match` to reduce bandwidth and API calls

### Stage 4: Analysis + inventory
- No additional permissions beyond having the YAML content.

### Stage 5: Enrichment (optional)
- Actions variables/secrets (repo/org) read to annotate “exists/unknown”
- Environment scope enrichment if available

---

## Recommended minimal permission profiles
### Profile 1: Inventory-only (lowest privilege)
- **Contents: Read**
- Plus whatever is minimally required for your chosen target discovery mode:
  - allowlist mode: nothing else
  - org/user enumeration: repo listing read access

### Profile 2: Inventory + validation (still read-only)
- Everything in Profile 1
- **Actions: Read** (optional)
- **Secrets (names): Read** at repo/org scope (optional)
- **Variables (names): Read** at repo/org scope (optional)

---

## Notes and caveats
- Permissions vary slightly between **fgPAT** and **GitHub App** UIs, but the conceptual mapping is the same: contents read is required for workflow file scanning.
- Some endpoints may be restricted by enterprise policies; the tool should degrade gracefully (skip enrichment and continue inventory from YAML).
- The tool should never request or store secret values; only reference names and usage locations are recorded.
