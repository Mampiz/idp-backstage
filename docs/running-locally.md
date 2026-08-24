# Running it locally

## What you need

`docker`, `kind`, `kubectl`, `go` 1.26, Node 22 with corepack, and the `gh` CLI.

## The token

The GitHub token is never written to disk, not even to the gitignored `.env`. It
is exported in the shell and read from the environment by everything that needs
it.

```bash
# The "workflow" scope is required: the scaffolded repository contains
# .github/workflows/ci.yml, and GitHub refuses to write anything under
# .github/workflows/ without it - reporting a bare 404 that looks like a
# missing repository. The gh CLI does not request it by default.
unset GITHUB_TOKEN
gh auth refresh -h github.com -s workflow
export GITHUB_TOKEN=$(gh auth token)
```

## Bring it up

```bash
cp .env.example .env    # non-secret local configuration
make bootstrap          # kind cluster + cert-manager + webapp-operator
make verify-f0          # prove that much works before going further
make dev                # Postgres, both Go services in the cluster, Backstage
```

Backstage is on <http://localhost:3000>. The Go services run inside the cluster
and are reachable from the host on NodePorts 30080 and 30081, mapped by the kind
configuration.

## Use it

Open **Create**, pick *Go service running on Kubernetes*, fill in the form. When
the task finishes you have a repository and a running workload; the **WebApp**
tab on the new component shows the live state.

To watch the tab follow the cluster:

```bash
kubectl --context=kind-idp-local -n idp-apps scale webapp <name> --replicas=4
```

## Tear it down

```bash
make cluster-down   # deletes the kind cluster
make db-nuke        # stops Postgres and deletes its volume
```

`make help` lists every target.
