# PROGRESS

State of the phase graph. A phase is not done until its verifier exits 0.

| Phase | What | State | Verifier |
|-------|------|-------|----------|
| F0 | Base: kind + operator + monorepo | ✅ **passes** | `make verify-f0` |
| F1 | Backstage base (Postgres, catalog, discovery) | ✅ **passes** | `make verify-f1` |
| F2 | [GO] status-api | ✅ **passes** | `make verify-f2` |
| F3 | [GO] scaffolder service | ✅ **passes** | `make verify-f3` |
| F4 | Backstage software template | ✅ **passes** | `make verify-f4` |
| F5 | webapp-status frontend plugin | ✅ **passes** | `make verify-f5` |
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

## F2 · STATUS API — ✅ passes

**Verifier:** `make verify-f2` → exit 0. It runs the unit tests, applies a real WebApp to
the cluster, starts the service against it, and then compares what the API says against
what `kubectl` says: ready replicas, image and the Available condition all have to match.
An API that only agrees with itself proves nothing.

### What I did

- `services/status-api`: a Go service reading WebApp custom resources through the
  **dynamic client**, with a shared informer per resource, exposing:
  - `GET /api/webapps` (optionally `?namespace=`)
  - `GET /api/webapps/{namespace}/{name}`
  - `GET /healthz`, `GET /readyz`, `GET /metrics`
- Distroless multi-stage image, plus manifests for running it inside the cluster with a
  dedicated ServiceAccount and a NodePort on 30081. Both modes are verified.
- Tests over the fake dynamic client and the fake clientset, covering the conversion, the
  owner-reference matching, a missing status, a missing Deployment and the 404 path.

### Design decisions

**1. Informers, not polling.**
Both resources are watched through shared informers, so reads are served from an in-memory
cache and the API server is not touched on the request path. The resync period is a safety
net against a missed watch event, not the mechanism.

**2. Readiness is tied to cache sync, and that distinction matters.**
`/readyz` returns 503 until both informer caches are populated. Serving an empty list from
a cold cache would read as "there are no WebApps", which is a different and far more
misleading answer than "not ready yet". The HTTP server deliberately starts before the
caches are warm so the probe can express that difference.

**3. Three replica counts, not one.**
The API reports `desired` (spec.replicas on the custom resource), `effective`
(spec.replicas on the Deployment) and `ready`. They are genuinely different numbers: when
autoscaling is enabled the operator **stops managing the Deployment's replica count** and
lets the HPA own it, so `desired` and `effective` legitimately diverge. Collapsing them
into one would make the UI lie during autoscaling. Same reasoning for `image.desired` vs
`image.deployed`, which differ mid-rollout.

**4. The Deployment is matched by owner reference, not by name.**
The operator names it `<webapp>-deployment`, but the ownership link is the actual contract
and does not break if that convention changes. A test asserts that a Deployment owned by
something else in the same namespace is not picked up.

**5. Typed client for Deployments, dynamic for the custom resource.**
The custom resource has to be dynamic: no generated types for it on this side, which keeps
this repository decoupled from the operator's Go module. Deployments are a core type and
digging `readyReplicas` out of an unstructured map would be noise for no benefit.

**6. The custom-resource RBAC is the operator's, not a copy of it.**
The operator already publishes `webapp-operator-webapp-viewer-role` with exactly
get/list/watch on webapps. The manifests bind that role rather than declaring a second copy
that would silently drift. Only the Deployment read, which that role does not cover, is
declared here.

**7. `go mod tidy` runs with `GOWORK=off`.**
Inside `go.work` the workspace supplies the build list, so `go.sum` can stay incomplete and
only fail later in a container build, where there is no workspace. That is exactly how the
first image build failed.

### Note

The distroless image declares its user by name (`nonroot`), and the kubelet cannot verify a
non-numeric user against `runAsNonRoot: true`, so the pod spec pins `runAsUser: 65532`,
the uid behind that name.

---

## F3 · SCAFFOLDER SERVICE — ✅ passes

**Verifier:** `make verify-f3` → exit 0. It creates a real repository in a real GitHub
account and a real workload in the cluster, then checks that the scaffolded files are
actually in the repository, that `kubectl` lists the custom resource, that the pods reach
Ready, that repeating the request is a no-op, and that a request the operator would reject
is refused before anything is created.

Proof of the last run: [Mampiz/idp-scaffold-demo](https://github.com/Mampiz/idp-scaffold-demo),
two commits (`Initialise repository`, `Scaffold idp-scaffold-demo`), its own CI green, and
`idp-apps/idp-scaffold-demo` running 2/2 pods in the cluster.

### What I did

`POST /scaffold` takes `{name, owner, repoUrl, image, port, replicas}` and, in order and
idempotently:

1. creates the GitHub repository and commits an embedded template (`go:embed`): a
   stdlib-only Go service with `/healthz`, `/readyz` and `/metrics`, a distroless
   Dockerfile, a Makefile, a workflow publishing to ghcr.io, `catalog-info.yaml`, and the
   `webapp.yaml` that runs it;
2. applies the matching WebApp custom resource with a server-side apply.

Both Go services now run **inside the cluster**, each with its own ServiceAccount bound to
the operator's published editor/viewer ClusterRoles, and are reachable from the host on
NodePorts 30080 and 30081 through the kind port mappings.

### Design decisions

**1. The failure policy: the repository is never deleted.**
This is the decision the brief asked to make and document. If step 2 fails, deleting the
repository would be irreversible, and if the name happened to be taken already it would
destroy somebody's work to tidy up after our own failure. So the run stops in a state that
is explicit and resumable:

- the repository is tagged with the `idp-provisioning-incomplete` topic, so the
  half-finished state is discoverable from GitHub itself without asking this service;
- the response is **207 Multi-Status**, naming the step that failed and returning the
  repository that does exist;
- re-sending the same request finishes the job, because every step is idempotent.

A successful run tags the repository `idp-managed` and clears the incomplete marker.
Failing to write a topic never masks the error that actually matters.

**2. Validation happens before anything is created.**
The image is checked against the operator's rule (explicit, non-latest tag) up front. This
is the F0 finding paying off: without it, a request would create a repository and then be
refused by admission, producing exactly the half-finished state the failure policy exists
to avoid. Every problem is reported at once so a form is not resubmitted one mistake at a
time.

**3. The scaffolded service has no dependencies at all.**
Metrics are written by hand in Prometheus exposition format rather than pulling in
`client_golang`. A counter and a duration total are not worth a dependency: there is no
`go.sum` to keep in sync and the generated repository builds offline. A test renders the
template into a temporary directory and runs `go vet` and `go test` on the result, so what
the template produces is proven to compile and pass its own tests.

**4. One commit through the git data API, not one commit per file.**
The contents API would produce a commit per file. The git data API writes a tree and a
single commit instead. It cannot operate on a repository with no commits at all
("409 Git Repository is empty"), so new repositories are created with auto-init and a
pre-existing empty one is bootstrapped through the contents API first.

**5. The token never touches a file.**
`make scaffolder-secret` pipes `kubectl create secret --dry-run=client -o yaml` into
`kubectl apply`, reading `$GITHUB_TOKEN` from the environment. Nothing is written to disk
at any point.

**6. `internal/k8s/client.go` is duplicated between the two services on purpose.**
They are separate Go modules with separate container build contexts. Sharing sixty lines
would mean either coupling both images to a repo-root build context or adding a third
module with a `replace` that breaks outside the workspace. The duplication is cheaper than
either.

### The `workflow` scope, and a 404 that means something else entirely

Worth recording because it cost real time. The push step failed with a **404 Not Found** from
`POST /repos/{owner}/{repo}/git/trees`, with an empty body. That reads like a missing
repository, and it is not: writing a tree that contains anything under
`.github/workflows/` requires the `workflow` scope, and GitHub reports its absence as a
bare 404 rather than a 403. Reproduced precisely:

```
CreateTree with entry "probe2.txt"                  -> 201 Created
CreateTree with entry ".github/workflows/probe.yml" -> 404 Not Found
```

The `gh` CLI does not request that scope by default. The fix:

```
unset GITHUB_TOKEN                            # gh refuses to refresh while it is set
gh auth refresh -h github.com -s workflow
export GITHUB_TOKEN=$(gh auth token)
```

The service no longer fails opaquely on this: the 404 is translated into a named error that
says what to do, and the token's scopes are checked at startup so the problem surfaces
before anyone's repository is half provisioned. Both behaviours are covered by tests.

---

## F4 · SOFTWARE TEMPLATE — ✅ passes

**Verifier:** `make verify-f4` → exit 0.

The brief's verifier is "fill in the form at `/create` and a repository, a custom resource
and pods come out, with nothing done by hand". A mouse cannot be scripted, but the form is
only a client of the scaffolder API: submitting it executes the template through
`POST /api/scaffolder/v2/tasks`. The verifier drives that exact path with the exact values
the form would send, so the same thing is proven. It also asserts that the form itself is
right — every field present, `owner` on the OwnerPicker, `repoUrl` on the RepoUrlPicker —
because driving the API would otherwise pass even if the form were broken.

Proof of the last run: [Mampiz/idp-template-demo](https://github.com/Mampiz/idp-template-demo)
and `idp-apps/idp-template-demo` running 2/2 pods, created from the template with no manual
step.

### The decision: a thin custom action, not the proxy

Both options were real, and the choice is not obvious.

**The proxy route** (`http:backstage:request`) is **not in Backstage core** — verified in
1.54: the only actions the core scaffolder backend ships are `dry-run`, `execute-template`,
`get-scaffolder-task-logs`, `list-scaffolder-actions` and `list-scaffolder-tasks`. It comes
from `@roadiehq/scaffolder-backend-module-http-request`, a maintained community module
(v5.8.0, June 2026) that does support the new backend system. Using it would mean routing
the Go service through `proxy.endpoints` and writing no TypeScript at all, which is
genuinely attractive under the project's design rule.

**I chose a thin custom action anyway**, for three reasons:

1. **The action needs typed outputs.** Later steps and the result page use
   `steps.scaffoldWebApp.output.repoUrl` and the namespace and name of the custom resource.
   A generic HTTP action returns `{code, headers, body}`, so those would have to be dug out
   of an untyped body inside template YAML.
2. **The error surface is the whole point.** The Go service answers 400 with a list of
   validation problems and **207 with a documented half-finished state**. Turning those into
   something a person can act on with a generic action means `if` conditions on
   `steps.x.output.code` inside YAML — which is logic, in the least testable place in the
   stack. In TypeScript it is five lines and five unit tests.
3. One less third-party dependency in the provisioning path.

**The action holds no business logic.** It builds a request, sends it, and maps status codes
to messages: about seventy lines. Validating the image against what the operator's webhook
will accept, the order of the steps, idempotency and the failure policy all stay in Go. Its
tests assert exactly that transport contract, including that a 207 explains which step
failed and that the repository was left in place.

### Other decisions

**1. The form validates the image tag too, and the Go service is still the authority.**
The `image` field carries a pattern that rejects `:latest` and untagged references. The
user finds out while typing instead of after a repository already exists. The Go service
validates it again, because a form is not a security boundary and the API is callable
directly.

**2. Step ids are camelCase, and the verifier enforces it.**
Inside `${{ steps.x.output.y }}` a hyphen is parsed as subtraction, so a step called
`scaffold-web-app` silently evaluates to `NaN` instead of failing. The verifier greps the
template for a hyphenated step id and fails if it finds one, so the rule survives future
edits rather than living in someone's memory.

**3. The template registers the component explicitly even though discovery would find it.**
The Go discovery service would pick the new repository up within its cache TTL. Registering
it in a `catalog:register` step just means the component is already there when the user
clicks through from the result page. It is marked `optional: true`, so a hiccup registering
does not fail a run whose real work already succeeded.

**4. The create-app example entities were dropped.**
Only `examples/org.yaml` is still registered, because the guest identity and the OwnerPicker
need a user and a group to point at. The example component and the example template are gone
now that a real one exists.

---

## F5 · WEBAPP STATUS PLUGIN — ✅ passes

**Verifier:** `make verify-f5` → exit 0. Seven steps, ending in a real browser: it opens the
entity page, asserts the tab renders what `kubectl` reports, then scales the custom resource
from outside Backstage entirely and watches the page follow it without a reload.

### What I did

A frontend plugin for the **new frontend system** (`createFrontendPlugin` +
`EntityContentBlueprint`) that adds a **WebApp** tab to entities carrying the
`platform.miportfolio.com/webapp` annotation, showing the Available condition, replicas, the
deployed image, the port, the workload name and the autoscaling window.

### Design decisions

**1. Through the Backstage proxy, not straight from the browser.**
The Go status API is not a browser-facing origin. Going through `proxy.endpoints` means no
CORS configuration, no second URL for the frontend to know about, and the browser only ever
talks to Backstage. The endpoint is restricted to `GET`.

**2. Polling every five seconds, not SSE.**
The state is a handful of fields that change on the timescale of a rollout; the Go API
already serves them from an informer cache, so a request costs the cluster nothing; and an
open stream per viewer would have to be reconnected through the proxy on every hiccup. If
this ever needs sub-second latency, the stream belongs in the Go service, not in the
browser. The interval is one constant, so changing the decision is a one-line change.

**3. The UI does not collapse numbers that are genuinely different.**
Desired and effective replicas are shown separately whenever they diverge — with autoscaling
on, the operator hands the Deployment's replica count to the HPA, so "2 desired" and
"7 running" are both true and mean different things. A rollout shows both the running image
and the one being rolled out to. Collapsing either would make the tab lie at exactly the
moment somebody is looking at it to find out what is going on.

**4. The tab is attached by annotation, not to every component.**
`EntityContentBlueprint` takes a filter, so components that are not deployed as a WebApp do
not grow an empty tab. An entity that has the annotation but no custom resource in the
cluster gets an explicit "not in the cluster" state, which is a different answer from "not
linked".

**5. In-flight responses are discarded on navigation.**
A generation counter makes a slow response for a previous entity unable to land on the
current one. Without it, clicking between two components fast enough shows one component's
numbers under the other's name.

**6. The plugin lives at `backstage/plugins/webapp-status`, not at the repo root.**
The brief's layout puts it at `/plugins/webapp-status`. A Backstage plugin has to be inside
the app's yarn workspace to be resolvable, and yarn workspace globs cannot reach outside the
project directory, so it sits in the Backstage workspace's own `plugins/` directory. It is
the same thing in the only place it can be.

### The browser test, and getting chromium to run without root

The unit tests prove the component renders the right strings; only a browser proves the tab
actually mounts and updates. Backstage's `generateProjects()` hard-defaults Playwright to the
`chrome` channel — a system-wide Google Chrome that needs root to install — and passing
`channel: undefined` does not help because the helper resolves it with `??` and falls back to
`"chrome"` again. The config now removes the key entirely unless `PLAYWRIGHT_CHANNEL` asks
for one, which selects Playwright's own bundled chromium.

That bundled chromium is still missing four shared objects
(`libnspr4`, `libnss3`, `libnssutil3`, `libasound`), and `playwright install-deps` shells out
to apt as root. `infra/scripts/playwright-libs.sh` fetches the three packages with
`apt-get download` — which needs no privileges, it only downloads `.deb` files — unpacks them
into a prefix under `node_modules/.cache`, and prints the `LD_LIBRARY_PATH` to use. No root
anywhere, and nothing outside the repository is touched.

---

## Continuous integration

Three workflows, split so a change in one part does not queue the others:

- **`go`** — gofmt, `go vet`, build, `go test -race` and golangci-lint, as a matrix over
  the two Go modules.
- **`backstage`** — install, type check, lint and unit tests.
- **`e2e`** — stands up a real kind cluster, installs cert-manager and the operator and
  runs the F0 verifier. This is the workflow that actually proves the platform works;
  the unit tests only prove the code compiles and behaves in isolation.
