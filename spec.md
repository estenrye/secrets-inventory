I need to build an agent that can monitor the usage of secrets and environment variables in GitHub Actions workflows.

The agent should be able to:
- Scan GitHub repositories for workflows
- Identify secrets and environment variables used in workflows
- Track which workflows use which secrets and environment variables
- Provide a dashboard or report of secret usage
- Alert when new secrets are detected or when secrets are used in unexpected ways

The agent should be able to do this with the least amount of permissions possible, ideally just read access to GitHub repositories.

# How to identify secrets and environment variables in workflows

## Secrets
Secrets are identified by the `secrets.` prefix in workflow files. For example:
```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Use secret
        run: echo ${{ secrets.MY_SECRET }}
```
Secrets can also be defined as repository variables in the GitHub UI, which would need to be fetched via the GitHub API.  These variables can be scoped at the environment, repository and organization levels.

## Environment variables
Environment variables are identified by the `${{ env. }}` prefix in workflow files. For example:
```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    env:
      MY_VAR: ${{ secrets.MY_SECRET }}
    steps:
      - name: Use environment variable
        run: echo $MY_VAR
```

Environment variables can also be defined as repository variables in the GitHub UI, which would need to be fetched via the GitHub API.  These variables can be scoped at the environment, repository and organization levels.


