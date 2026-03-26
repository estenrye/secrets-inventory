# References

## AST
**AST** stands for **Abstract Syntax Tree**.

In this project (GitHub Actions workflow scanning), “AST-aware scanning” means:

- Parse the workflow YAML into a structured tree representation (maps/lists/scalars), instead of treating the file as plain text.
- Walk that tree to find and analyze strings in specific workflow fields, such as:
  - `jobs.<job>.steps[*].run`
  - `jobs.<job>.steps[*].env`
  - `jobs.<job>.steps[*].with`

This is usually more accurate than regex-only scanning because:

- You can tell *where* a value came from (job vs step vs env), which improves reporting.
- You get better context for policy checks (e.g., whether a secret is used in `run:` vs passed to an action input).

A raw-text (regex) fallback is still useful to catch edge cases where parsing fails or where references appear in unusual places.

## ETag
An **ETag** (entity tag) is an HTTP response header that acts like a fingerprint for a specific version of a resource (for example, a workflow file at a particular commit).

Clients can use it for caching by making a **conditional request**:

- First request: the server returns the content plus an `ETag` header.
- Next request: the client sends `If-None-Match: <etag>`.
- If the resource is unchanged, the server can respond with **`304 Not Modified`** and no body.
- If it changed, the server returns **`200 OK`** with the new content and a new `ETag`.

In this project, using ETags when fetching workflow files from GitHub reduces API usage and speeds up scans by avoiding downloading workflow YAML that hasn’t changed.
