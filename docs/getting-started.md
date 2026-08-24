# Getting started

Everything runs on your machine: a local Kubernetes cluster, the operator, two
Go services and Backstage. Nothing touches a remote cluster at any point — the
tooling pins the kind context explicitly and refuses to run against an API
server that is not local.

## Prerequisites

| Tool | Why |
|------|-----|
| Docker | the cluster, Postgres and the container builds |
| [kind](https://kind.sigs.k8s.io/) | the local Kubernetes cluster |
| kubectl | talking to it |
| Go 1.26 | the two Go services |
| Node 22 with corepack | Backstage |
| [gh](https://cli.github.com/) | the GitHub token |

## The GitHub token

The platform creates repositories, so it needs a token. It is never written to
disk — not even to the gitignored `.env` — and is read from the environment by
everything that needs it.

```bash
unset GITHUB_TOKEN                          # gh will not refresh while it is set
gh auth refresh -h github.com -s workflow
export GITHUB_TOKEN=$(gh auth token)
```

!!! warning "The `workflow` scope is not optional"

    Scaffolded repositories contain `.github/workflows/ci.yml`, and GitHub
    refuses to write anything under `.github/workflows/` without that scope —
    reporting a bare **404** that reads like a missing repository. The `gh` CLI
    does not request it by default. A classic PAT works too, with `repo`,
    `workflow` and `read:org`.

## Bring it up

```bash
cp .env.example .env    # non-secret local configuration
make bootstrap          # kind cluster + cert-manager + webapp-operator
make dev                # Postgres, both Go services, Backstage
```

`make bootstrap` takes about 75 seconds from nothing. When `make dev` finishes
starting, Backstage is on <http://localhost:3000>.

## Check it worked

```bash
make verify-f0
```

That applies a real `WebApp` to the cluster, waits for its pods, and confirms
the operator's admission webhooks are refusing images they should refuse. If it
exits zero, the platform underneath Backstage is sound.

## What is running where

| | Where | Port |
|---|---|---|
| Backstage | your machine | 3000 (UI), 7007 (backend) |
| Postgres | Docker | 5432 |
| scaffolder | in the cluster | 30080 |
| status-api | in the cluster | 30081 |

The two Go services run **inside** the cluster because they need cluster
credentials; the kind configuration maps their NodePorts to your machine so
Backstage can reach them without a port-forward.

## Tear it down

```bash
make cluster-down   # delete the kind cluster
make db-nuke        # stop Postgres and delete its volume
```

`make help` lists every target.
