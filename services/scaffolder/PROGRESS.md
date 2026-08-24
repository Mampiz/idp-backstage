# PROGRESS

State of the phase graph. A phase is not done until its verifier exits 0.

| Phase | What | State | Verifier |
|-------|------|-------|----------|
| F0 | Base: kind + operator + monorepo | ✅ **passes** | `make verify-f0` |
| F1 | Backstage base (Postgres, catalog, discovery) | ✅ **passes** | `make verify-f1` |
| F2 | [GO] status-api | ⬜ pending | `go test ./...` + `curl :8081/api/webapps` |
| F3 | [GO] scaffolder service | ⬜ pending (checkpoint) | a curl creates a real repo + CR + Ready pods |
| F4 | Backstage software template | ⬜ pending | the form in `/create` produces repo + CR + pods |
| F5 | webapp-status frontend plugin | ⬜ pending (checkpoint) | the tab tracks a real `kubectl scale` |
| F6 | Wrap-up: TechDocs, README, diagram, GIF | ⬜ pending | — |

---

## F0 · BASE — ✅ passes

**Verifier:** `make verify-f0` → exit 0.
A full rebuild from nothing (`make cluster-down && make bootstrap && make verify-f0`)
was measured at **75 seconds**, with no manual steps.

### What I did

- Dedicated `kind` cluster **`idp-local`** (`infra/kind/idp-local.yaml`), node image
  pinned by digest (`kindest/node:v1.36.1@sha256:21c46cf6…`), with port mappings
  30080/30081 reserved for exposing WebApps in later demos.
- **cert-manager v1.21.1** installed before the operator (see decision 2).
- **webapp-operator v1.0.0** installed from its own `dist/install.yaml` at a fixed tag.
- Supplemental RBAC for the operator's events (`infra/operator/events-rbac.yaml`, decision 4).
- Monorepo: `go.work` plus two Go modules, a self-documenting root `Makefile`,
  and `.gitignore` + `.env.example` from the first commit.
- Verifier `infra/scripts/verify-f0.sh`: six assertions, exit code as the verdict.

### Design decisions

**1. A new cluster `idp-local` rather than reusing `webapp-dev`.**
A `webapp-dev` cluster already existed from the operator's own development, with the
CRD applied by hand and neither cert-manager nor the operator deployed. Reusing it
would have made the platform depend on undocumented manual state. `idp-local` is
built entirely from the Makefile. `webapp-dev` is left untouched.

**2. cert-manager is a hard prerequisite of the operator, and it is undocumented.**
`dist/install.yaml` contains `Certificate`/`Issuer` objects from `cert-manager.io/v1`,
and both webhook configurations carry the `cert-manager.io/inject-ca-from` annotation.
Without cert-manager the `caBundle` stays empty and, since the webhooks use
`failurePolicy: fail`, **every `kubectl apply` of a WebApp is rejected**. The operator's
README lists only "a cluster and kubectl" as prerequisites. `make bootstrap` installs it,
and the verifier asserts the `caBundle` was injected, because that is the most likely
failure and the hardest one to diagnose from the error message.

**3. No image in this repository will ever use `:latest`.**
The operator's validating webhook (`validateImageTag`) rejects both `:latest` and
implicit tags. Worth noting: `config/samples/platform_v1_webapp.yaml` and the README
quick-start in the operator repo both use `nginx:latest`, so **the official example does
not pass its own validation**. Consequences downstream, already noted for F3/F4:
- the Go scaffolder must validate the tag *before* applying the CR and return a readable
  400 instead of letting it blow up in admission;
- the Backstage form needs validation on the `image` field.
Step 5 of the F0 verifier is a negative assertion confirming the webhook really does
reject `:latest`, so admission is proven to work, not just the happy path.

**4. Events RBAC: an additive patch on my side, the operator is not modified.**
The controller uses an `EventRecorder` (`DeploymentReconciled`, `ServiceReconciled`,
`HPAReconciled`, `ReconcileFailed`) but `webapp-operator-manager-role` has no rule for
`events`, so the API server rejected every single one with `events is forbidden`.
Reconciliation worked regardless, but `kubectl describe webapp` showed nothing and F5
would have had no events to render. Since the operator repository is off limits, a
supplemental ClusterRole and binding live in `infra/operator/events-rbac.yaml`, applied
by `make operator-install` and documented with the reason and the condition for deleting
it. **This is a bug in the operator**: the real fix is a rule in its `config/rbac/role.yaml`
followed by regenerating `dist/install.yaml`.

**5. Child object naming: `<webapp>-deployment` / `-service` / `-autoscaler`.**
The operator does not name the objects it owns after the WebApp itself; it appends a
suffix. This is encoded in the verifier and is something **F2 (status-api) and F5 (the
plugin) both need** in order to resolve a WebApp to its actual workload. Recorded here
so it does not have to be rediscovered.

**6. Every `kubectl` call passes `--context=kind-idp-local` explicitly.**
Nothing depends on the active context, and the verifier aborts unless the API server is
on `127.0.0.1`/`localhost`. It is not possible for this tooling to touch a remote cluster
by accident.

### Notes carried forward

- There is no `metrics-server` in the cluster, so the HPA reports `cpu: <unknown>`. It does
  not affect F0 (the HPA is created and reconciled). Showing real autoscaling in F5/F6 would
  need `metrics-server` with `--kubelet-insecure-tls`.
- The operator already ships `webapp-operator-webapp-editor-role` and `-viewer-role`
  ClusterRoles, reusable for the service accounts of the Go services.

---

## F1 · BACKSTAGE BASE — ✅ passes

**Verifier:** `make verify-f1` → exit 0. It checks that Backstage boots against Postgres
(not SQLite), that both frontend and backend answer, that the catalog serves the
`webapp-operator` Component through the API, that GitHub discovery is live, and that the
data is still in Postgres **with Backstage shut down** after the container is restarted.

### What I did

- `npx @backstage/create-app` → Backstage **1.54.0**, which already ships the New Backend
  System (`backend.add(import(...))` in `packages/backend/src/index.ts`). There is no
  `createRouter` and no legacy backend anywhere.
- **Postgres 17.6** through `docker-compose.yaml` with a named volume `idp-pgdata`,
  replacing the template's `better-sqlite3 :memory:`.
- Own entities in `catalog/webapp-operator.yaml`: the `webapp-operator` Component, the
  `idp` System and the `platform` Group, registered as a static location.
- A `catalog-info.yaml` at the root of this repository, so the platform is catalogued by
  exactly the same mechanism it offers to everyone else.
- **A Go discovery service** (see decision 4), containerised in `docker-compose.yaml` on a
  distroless image (25 MB).

### Design decisions

**1. The GitHub token does not live in `.env`, it lives in the shell environment.**
The rule is "environment variables, never on disk". `.env` is gitignored but it is still
disk, so `.env` holds non-secret configuration only (Postgres, owner) and `GITHUB_TOKEN`
has to be exported: `export GITHUB_TOKEN=$(gh auth token)`. The Makefile has a
`require-github-token` guard that fails with instructions. Backstage will not start
without it either: `integrations.github[0].token` is type-checked and an empty string is
a startup error, not a warning.

**2. Persistence is verified with Backstage switched off.**
Restarting the container and asking the API again proves nothing: the entity comes from a
static location and would simply be re-ingested. The verifier kills Backstage, restarts
the container, and reads the row straight out of `final_entities` with `psql`. If the row
is still there, it is because it is in the volume.

**3. A static external-access token for the verifiers.**
`backend.auth.externalAccess` of type `static`, fed by `BACKSTAGE_VERIFY_TOKEN`, so scripts
can call the catalog API without a browser session. It is the supported route; the
alternative (`dangerouslyDisableDefaultAuthPolicy`) turns off authentication for the whole
backend and is not something I want even locally.

**4. GitHub discovery does not work for personal accounts, so it is written in Go.**
`Mampiz` is a **user** account, not an organization, and Backstage's `GithubEntityProvider`
can only discover organizations. Verified both in the installed package and against the
live API:

```
$ grep -c 'organization(login: $org)' \
    node_modules/@backstage/plugin-catalog-backend-module-github/dist/lib/github.cjs.js
5                       # every GraphQL query the provider issues is organization(login:)
$ gh api graphql -f query='{ organization(login: "Mampiz") { login } }'
NOT_FOUND: Could not resolve to an Organization with the login of 'Mampiz'
```

There is no `user(login:)` and no `repositoryOwner` in the package: this is not a
configuration problem, the capability does not exist. The options were to create a GitHub
organization, to drop automatic discovery, or to write the discovery myself. Writing it in
Go is the one that matches the master rule and keeps the repositories in the personal
account, so `services/scaffolder` now exposes `GET /catalog/discovery`, which:

- resolves the owner and lists its repositories (works for both a user and an
  organization, so nothing breaks if the repos ever move into an org);
- keeps the ones that contain the catalog file on their default branch, skipping archived
  and disabled ones;
- renders a Backstage `Location` entity whose targets Backstage then processes recursively.

Two details that are deliberate:

- **A TTL cache (5 minutes).** Backstage refreshes locations on its own schedule and there
  is no reason to spend GitHub rate limit on every refresh.
- **Stale results are served when GitHub fails.** Returning an empty target list during a
  GitHub outage would orphan every entity ingested from it. A stale list is far less
  harmful than an empty one, and this is covered by a test.

The endpoint lives in the scaffolder service rather than a third one because that service
is the owner of everything this platform does against GitHub; F3 adds repository creation
to it. Backstage reads it through `backend.reading.allow`, and the TypeScript side contains
no logic at all: it is a URL in `app-config.yaml`.

### What is left

- The template's `examples/` (org.yaml, entities.yaml, template.yaml) is still registered.
  It will be dropped in F4 once the real template replaces it.

---

## Continuous integration

Three workflows, split so a change in one part does not queue the others:

- **`go`** — gofmt, `go vet`, build, `go test -race` and golangci-lint, as a matrix over
  the two Go modules.
- **`backstage`** — install, type check, lint and unit tests.
- **`e2e`** — stands up a real kind cluster, installs cert-manager and the operator and
  runs the F0 verifier. This is the workflow that actually proves the platform works;
  the unit tests only prove the code compiles and behaves in isolation.
