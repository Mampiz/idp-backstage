# idp-backstage

An Internal Developer Platform built on Backstage whose scaffolder does not stop
at creating a repository: it applies a `WebApp` custom resource that is
reconciled by a Kubernetes operator of my own,
[Mampiz/webapp-operator](https://github.com/Mampiz/webapp-operator).

Filling in a form in Backstage produces a repository, a running Deployment, a
Service, an HPA, and an entry in the catalog that reflects the real state of the
cluster. No manual step in between.

Phase-by-phase status and every design decision: [PROGRESS.md](PROGRESS.md).

## Design rule

Backstage core is TypeScript and that cannot be avoided. Everything else is Go:
if a piece of logic can live in a Go service, it lives in a Go service, and the
TypeScript side is only ever an HTTP client to it.

## Running it locally

```bash
export GITHUB_TOKEN=$(gh auth token)   # never written to disk, not even to .env
cp .env.example .env                   # non-secret local config
make bootstrap                         # kind + cert-manager + webapp-operator
make verify-f0                         # phase 0 verifier
make dev                               # Postgres, the Go services and Backstage
```

`make help` lists every target.

## Layout

```
backstage/              Backstage app (TypeScript, kept to a minimum)
services/status-api/    [GO] reads WebApp CRs with client-go, serves them over REST
services/scaffolder/    [GO] GitHub-facing service: catalog discovery, repo creation,
                             and applying the WebApp custom resource
plugins/webapp-status/  [TS] frontend plugin rendering the live state
catalog/                catalog entities owned by this repo
infra/                  kind cluster, operator install, phase verifiers
docs/                   TechDocs
```

## Verifiers

Each phase is only done when its verifier exits 0. They are real commands, not
opinions, and they run in CI as well as locally.

| Command | What it proves |
|---------|----------------|
| `make verify-f0` | The operator is installed and reconciles a WebApp into Ready pods |
| `make verify-f1` | Backstage runs on Postgres, discovery works, and the catalog survives a database restart |
| `make verify-f2` | The status API reports what `kubectl` reports, compared field by field |
