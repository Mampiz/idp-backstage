# Troubleshooting

The failures you are most likely to hit, and what they actually mean. Most of
them are cases where the error message points somewhere other than the problem.

## A bare 404 when creating a repository

```
POST https://api.github.com/repos/OWNER/NAME/git/trees: 404 Not Found []
```

**The token is missing the `workflow` scope.** Scaffolded repositories contain
`.github/workflows/ci.yml`, and GitHub refuses to write anything under
`.github/workflows/` without it — reporting 404 rather than 403, which reads like
the repository does not exist.

```bash
unset GITHUB_TOKEN
gh auth refresh -h github.com -s workflow
export GITHUB_TOKEN=$(gh auth token)
make scaffolder-deploy      # refresh the Secret in the cluster
```

The scaffolder checks its token's scopes at startup and translates this
particular 404, so it should tell you before you hit it.

## Every WebApp is rejected on apply

```
Internal error occurred: failed calling webhook "mwebapp-v1.kb.io":
  ... x509: certificate signed by unknown authority
```

**cert-manager is missing or has not injected the CA bundle yet.** The operator's
install manifest ships `Certificate` and `Issuer` objects and its webhooks fail
closed, so without cert-manager nothing can be created at all.

```bash
make cert-manager      # installs it and waits for the webhook to serve
```

If you see `connection refused` instead of a certificate error, the webhook pod
is up but not yet accepting connections. `make cert-manager` and
`make operator-install` both end by waiting for the webhook to admit a
server-side dry run, so run them rather than applying manifests by hand.

## The image is rejected

```
spec.image "nginx:latest" uses the mutable "latest" tag
```

Working as intended. Pin a tag or a digest. See
[about the image](creating-a-service.md#about-the-image).

## The repository exists but nothing is running

The response was **207 Multi-Status** and the repository is tagged
`idp-provisioning-incomplete`.

The repository was created and the custom resource could not be applied. Nothing
is deleted automatically — deleting a GitHub repository is irreversible, and if
the name was already taken it would destroy work that was never ours. The run is
resumable instead:

```bash
# Re-submitting the same form, or re-sending the same request, finishes the job.
curl -X POST localhost:30080/scaffold -H 'Content-Type: application/json' \
  -d '{"name":"...","image":"...","port":8080,"replicas":2}'
```

Every step is idempotent, so the repository is left alone and only the missing
custom resource is applied. On success the marker topic is cleared and replaced
with `idp-managed`.

## The WebApp tab is not there

The tab is attached only to entities carrying the annotation:

```yaml
metadata:
  annotations:
    platform.miportfolio.com/webapp: idp-apps/my-service
```

Scaffolded services get it automatically. A component imported by hand needs it
added to its `catalog-info.yaml`.

If the tab is there but says **"No WebApp … in the cluster"**, the entity points
at a custom resource that is not there — deleted, or never applied.

## The tab shows stale numbers

It polls every five seconds. If it is stale for longer than that, check the
status API directly:

```bash
curl localhost:30081/api/webapps/idp-apps/<name>
curl localhost:30081/readyz          # 503 while its caches are still syncing
```

A 503 from `/readyz` means the informer caches have not populated yet, which is
deliberately distinguished from "there are no WebApps".

## Backstage will not start

```
Invalid type in config for key 'integrations.github[0].token', got empty-string
```

`GITHUB_TOKEN` is not exported. It is deliberately not stored in `.env`; export
it in your shell.

```
SASL: SCRAM-SERVER-FIRST-MESSAGE: client password must be a string
```

The Postgres variables are not in the environment. `make dev` loads `.env` for
you; if you are running `yarn start` by hand, source it first.

## Ports are already in use

`make dev` needs 3000 and 7007. The verifiers refuse to start if either is
occupied rather than producing a confusing failure later. A previous run that was
interrupted can leave processes behind:

```bash
pkill -f 'backstage-cli repo start'
pkill -x status-api
```

## The HPA shows `cpu: <unknown>`

A kind cluster has no `metrics-server`. The HPA is created and reconciled
correctly; it just has no metrics to act on. Install `metrics-server` with
`--kubelet-insecure-tls` if you want to see it scale.
