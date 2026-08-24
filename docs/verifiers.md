# The phases and their verifiers

The platform was built as a sequence of phases, and a phase was not considered
done until a command exited zero. Not a screenshot, not a demo that worked once:
a command, in the repository, that anybody can run.

| Command | What it actually proves |
|---------|-------------------------|
| `make verify-f0` | The operator is installed and reconciles a real `WebApp` into Ready pods, the admission webhooks have a CA bundle, and an image with `:latest` is **rejected** |
| `make verify-f1` | Backstage boots on Postgres, the catalog serves the operator entity, GitHub discovery reaches the catalog, and the data survives a database restart |
| `make verify-f2` | The status API's replicas, image and condition match what `kubectl` reports, field by field |
| `make verify-f3` | One `curl` produces a real repository with real content, a real custom resource, and Ready pods — and repeating it is a no-op |
| `make verify-f4` | Executing the software template does all of the above with nothing done by hand, and the form has the fields and pickers it should |
| `make verify-f5` | A real browser opens the WebApp tab and watches it follow a `kubectl scale` |

## What makes them worth having

**They assert against the cluster, not against themselves.** The F2 verifier
compares the API's answer with `kubectl`'s. An API that agrees with itself proves
nothing.

**They include negative assertions.** F0 asserts that a `WebApp` with a `:latest`
image is *refused*. Without that, the phase would pass on a cluster whose
admission webhooks were not working at all — which is exactly the failure mode
that is hardest to notice.

**They test the claim, not a proxy for it.** F5's last step is a real browser,
because "the tab shows it" is a different statement from "the API returns it".

**They enforce rules that would otherwise live in someone's memory.** F4 greps
the template for a hyphenated step id, because inside `${{ steps.x.output.y }}` a
hyphen is parsed as subtraction and silently evaluates to `NaN`.

## In CI

Three workflows: `go` (fmt, vet, build, `test -race`, golangci-lint over both
modules), `backstage` (install, type check, lint, unit tests) and `e2e`, which
stands up a real kind cluster, installs cert-manager and the operator, and runs
the F0 verifier. The e2e job is the one that proves the platform works; the unit
tests only prove the code compiles and behaves in isolation.
