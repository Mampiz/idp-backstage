# Decisions worth arguing about

The full record, phase by phase, is in
[PROGRESS.md](https://github.com/Mampiz/idp-backstage/blob/main/PROGRESS.md).
These are the ones where the reasoning matters more than the outcome.

## The failure policy: a repository is never deleted

`POST /scaffold` creates a GitHub repository and then applies a custom resource.
If the second step fails, the obvious move is to roll back the first.

It does not. Deleting a GitHub repository is irreversible, and if the name
happened to be taken already, rolling back would destroy somebody else's work to
tidy up after our own failure. So the run stops in a state that is **explicit and
resumable**:

- the repository is tagged with the `idp-provisioning-incomplete` topic, so the
  half-finished state is discoverable from GitHub itself without asking this
  service anything;
- the response is `207 Multi-Status`, naming the step that failed and returning
  the repository that does exist;
- re-sending the same request finishes the job, because every step is idempotent.

The trade-off is real: an abandoned failed run leaves a repository behind. That
is a cost worth paying to never delete something we did not create.

## GitHub discovery does not work for personal accounts

Backstage's `GithubEntityProvider` can only discover repositories inside an
organization — every GraphQL query it issues is `organization(login: $org)`, and
against a user account it returns `NOT_FOUND`. This is not a configuration
problem; the capability does not exist.

The options were: create a GitHub organization, drop automatic discovery, or
write it. Writing it in Go matches the design rule and keeps the repositories
where they are, so the scaffolder serves `GET /catalog/discovery`, which lists
the account's repositories, keeps the ones carrying a catalog file, and renders a
Backstage `Location` entity. Backstage consumes it as an ordinary url location.

Two details that are deliberate: results are cached for a TTL, because Backstage
refreshes locations on its own schedule and that should not cost GitHub rate
limit; and **when GitHub fails, the last good result is served instead of an
empty one**, because an empty target list would orphan every entity ingested from
the location.

## A thin custom action instead of the proxy

`http:backstage:request` is not in Backstage core; it comes from a community
module. Routing the Go service through `proxy.endpoints` and using it would mean
writing no TypeScript at all, which the design rule favours.

The custom action won anyway, for one reason that matters: the Go service answers
`400` with a list of validation problems and `207` with a documented
half-finished state. Turning those into something a person can act on with a
generic HTTP action means `if` conditions on a status code **inside template
YAML** — logic, in the least testable place in the stack. In TypeScript it is five
lines and five unit tests.

The action holds no business logic. Validation, ordering, idempotency and the
failure policy all stay in Go.

## Polling, not SSE

The WebApp tab polls every five seconds. The state is a handful of fields that
change on the timescale of a rollout; the Go API serves them from an informer
cache, so a request costs the cluster nothing; and an open stream per viewer
would have to be reconnected through the Backstage proxy on every hiccup.

If this ever needs sub-second latency, the stream belongs in the Go service, not
in the browser. The interval is one exported constant, so changing the decision
is a one-line change.

## Numbers that are different are shown as different

The status API reports `desired` (what the custom resource asks for), `effective`
(what the Deployment is set to) and `ready`. With autoscaling enabled the operator
hands the Deployment's replica count to the HPA, so `desired` and `effective`
diverge legitimately. Collapsing them into one number would make the UI lie at
exactly the moment somebody is looking at it to find out what is going on.

The same reasoning applies to `image.desired` versus `image.deployed` during a
rollout.

## Things found in the operator while consuming it

The operator repository is never modified. Two problems in it were found and
handled on this side:

- **`dist/install.yaml` requires cert-manager**, which its README does not list
  as a prerequisite. Without it the webhook `caBundle` stays empty and, because
  the webhooks fail closed, *every* `WebApp` apply is rejected. `make bootstrap`
  installs it and the F0 verifier asserts the bundle was injected, because that
  is the most likely failure and the hardest to read from the error message.
- **The operator's ClusterRole has no rule for `events`**, so every event its
  recorder emits is rejected by the API server. Reconciliation still works, but
  `kubectl describe webapp` shows nothing. A supplemental ClusterRole is applied
  here, documented with the reason and the condition for deleting it. The real
  fix belongs upstream.

Its shipped sample also uses `nginx:latest`, which its own validating webhook
rejects — the official example does not pass its own validation.

## Two racing conditions that only CI found

Both are the same mistake in different places: **"the Deployment is Available" is
not "the thing is serving"**.

- cert-manager's Deployment reports Available before its webhook has ready
  endpoints, so applying the operator's manifest immediately after fails with
  `connection refused`. Installation now ends by asking the webhook to admit a
  throwaway resource with a server-side dry run.
- `kubectl wait` errors out immediately when the resource does not exist yet
  rather than waiting for it, and the operator creates the Deployment a moment
  after the custom resource is admitted. The verifiers poll for existence first.

Both passed locally for weeks of wall-clock time because the steps were run
seconds apart by hand. CI runs them back to back.
