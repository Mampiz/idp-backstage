# Contributing

## Repository layout

```
backstage/                    the Backstage app (TypeScript, kept minimal)
  packages/backend/             includes the platform:scaffoldWebApp action
  plugins/webapp-status/        the WebApp entity tab
  demo/                         scripted walkthrough and screenshots
services/status-api/    [GO]  reads WebApp CRs, serves them over REST
services/scaffolder/    [GO]  GitHub: discovery, repo creation, applying the CR
templates/webapp-service/     the software template
catalog/                      catalog entities owned by this repo
infra/                        kind cluster, operator install, scripts
docs/                         this documentation
```

The two Go services are separate modules tied together by `go.work`, and each has
its own container build context. That is why the small kubeconfig helper is
duplicated between them rather than shared: a third module with a `replace`
directive would break outside the workspace, and coupling both images to a
repository-root build context costs more than sixty duplicated lines.

## Running the tests

```bash
make test                            # Go unit tests, both modules
cd backstage && yarn backstage-cli repo test   # TypeScript
make build                           # both Go services
```

Linting is `golangci-lint` for Go (config at the repository root, shared by both
modules) and `backstage-cli repo lint` for TypeScript.

## End-to-end checks

Each part of the platform has a command that exits zero only when it genuinely
works. They run against a real cluster and assert against `kubectl` rather than
against themselves.

| Command | What it proves |
|---------|----------------|
| `make verify-f0` | The operator reconciles a real `WebApp` into Ready pods, and refuses a `:latest` image |
| `make verify-f1` | Backstage runs on Postgres, discovery reaches the catalog, and the data survives a database restart |
| `make verify-f2` | The status API's replicas, image and condition match `kubectl` field by field |
| `make verify-f3` | One request produces a real repository, a real custom resource and Ready pods — twice, idempotently |
| `make verify-f4` | The software template does the same with nothing done by hand |
| `make verify-f5` | A real browser opens the WebApp tab and watches it follow a `kubectl scale` |
| `make verify-f6` | The documentation builds, the portal serves it, and the README carries the diagram and the demo |

They create real repositories in the configured GitHub account, so run them
knowingly.

Three of them are worth understanding before changing anything:

- **`verify-f0` includes a negative assertion.** It fails if a `WebApp` with a
  `:latest` image is *accepted*. Without that, the check would pass on a cluster
  whose admission webhooks were not working at all, which is the failure that is
  hardest to notice.
- **`verify-f1` verifies persistence with Backstage shut down.** Restarting the
  database and asking the API again proves nothing, because a static location
  would simply be re-ingested. It reads the row out of Postgres directly.
- **`verify-f4` greps the template for hyphenated step ids.** Inside
  `${{ steps.x.output.y }}` a hyphen is parsed as subtraction, so a step called
  `scaffold-web-app` silently evaluates to `NaN` instead of failing.

## Continuous integration

Three workflows, split so a change in one part does not queue the others:

- **`go`** — gofmt, `go vet`, build, `go test -race` and golangci-lint, as a
  matrix over the two modules.
- **`backstage`** — install, type check, lint, unit tests.
- **`e2e`** — stands up a real kind cluster, installs cert-manager and the
  operator, and runs `verify-f0`. This is the job that proves the platform works;
  the unit tests only prove the code compiles and behaves in isolation.

## Regenerating the documentation media

```bash
make screenshots   # needs Backstage running
make record-demo   # re-records the GIF; creates a real repository
```

Both are scripted so they can be refreshed when the UI changes, rather than
ageing into pictures of software that no longer looks like that.

## Working on the operator

Don't, from here. [webapp-operator](https://github.com/Mampiz/webapp-operator) is
consumed as a dependency and never modified by this repository. Where it has
gaps, they are worked around on this side and documented in
[design decisions](decisions.md#things-found-in-the-operator-while-consuming-it),
with the upstream fix named.

## How this was built

The build log, phase by phase with the reasoning and the dead ends, is in
[`PROGRESS.md`](https://github.com/Mampiz/idp-backstage/blob/main/PROGRESS.md).
