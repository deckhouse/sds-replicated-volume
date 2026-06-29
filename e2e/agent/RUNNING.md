# Running the Agent E2E Suite

Runbook for executing `e2e/agent` against a remote cluster reachable via
`kubectl`, to verify a local change to `images/agent`. See
[`TESTCASES.md`](TESTCASES.md) for _what_ the suite covers.

## Ground rules

- The suite is expected to pass on `HEAD`. Treat any failure as a regression.
- The suite cleans up on success and on failure. Anything `e2e-*` left in the
  cluster is a real bug — capture state and report before forcing cleanup.
- **Never strip a finalizer manually.** The agent must release its own
  finalizers; bypassing that hides cleanup-path bugs (LLV finalizer leaks,
  controller-side ordering, etc.).
- Don't assume the cluster matches `HEAD`. Drift in CRDs, base image and kernel
  vs. userspace tooling is the most common cause of failures from a fresh
  `HEAD` build — check those before suspecting code.

## Configuration

`e2e/agent/.env.json` must exist with cluster-specific node/LVG names. If it
does not, copy and edit `.env.example.json`.

## Run it

These three steps assume `$USER` is unique enough to namespace your dev image
tag. Run from the repo root.

```bash
# 1) Apply the repo's CRDs to the cluster (asks for user confirmation).
#    Required whenever the API package changed since the cluster's module
#    version. Idempotent.
kubectl apply -f crds/storage.deckhouse.io_*.yaml

# 2) Build, push and roll the agent. Image lands at
#    registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-agent
docker build \
  -t registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-agent \
  -f images/agent/dev/Dockerfile-dev .
docker push registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-agent
kubectl -n d8-sds-replicated-volume rollout restart ds agent
kubectl -n d8-sds-replicated-volume rollout status  ds agent --timeout=180s

# 3) Run the suite
( cd e2e/agent && go test -timeout 30m -v ./... ) | tee /tmp/run.log
```

If Deckhouse keeps reverting your changes (CRDs, daemonset image), scale it
down and pin the daemonset for the session:

```bash
kubectl -n d8-system scale deploy deckhouse --replicas=0
kubectl -n d8-sds-replicated-volume patch ds agent --type=json -p='[
  {"op":"add","path":"/spec/template/spec/imagePullSecrets",
   "value":[{"name":"fox-registry"}]}]'
```

(Re-scale Deckhouse back up when done.)

## `Dockerfile-dev` (gitignored, recreate as needed)

`images/agent/dev/Dockerfile-dev` is a local-only file. If absent, write it.
Bump `golang:` to match `images/agent/go.mod` and `FROM <module image>@<digest>`
to a current digest (see "Picking a base-image digest" below). Skeleton:

```dockerfile
FROM golang:1.25.10-alpine3.22 AS builder
WORKDIR /go/src
COPY api/        /api/
COPY lib/go/     /lib/go/
COPY images/agent/go.mod images/agent/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY images/agent/ .
WORKDIR /go/src/cmd
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o agent

FROM dev-registry.deckhouse.io/sys/deckhouse-oss/modules/sds-replicated-volume@sha256:<DIGEST>
COPY --from=builder /go/src/cmd/agent /agent
ENTRYPOINT ["/agent"]
```

The base image is the **production agent image** (`helm_lib_module_image
"agent"`): distroless plus the relocated drbd-utils (`/sbin/drbdsetup`,
`drbdadm`, `drbdmeta`) and `/usr/sbin/dmsetup`. Basing on it guarantees the dev
binary runs against the exact userspace tooling production ships (the agent
shells out to `dmsetup`, so a base lacking it — e.g. the
`drbd-prometheus-exporter`/`drbdReactor` image — is wrong). The Go binary is
fully static, so only `/agent` is overwritten.

Because the base is distroless there is **no `/bin/sh`** and no `blockdev`
inside the running agent container. `kubectl exec`/`NodeExec` must invoke
binaries directly (`dmsetup`, `drbdsetup`, …) — never via `sh -c`.

## Drift to check before suspecting code

These are the three sources of drift seen in practice. Check in this order.

### Builder Go version

If `docker build` fails because the builder Go is older than `go.mod` requires,
bump the `golang:` tag in `Dockerfile-dev`.

### Cluster CRDs older than the repo

Symptom: every test times out at the first wait; the DRBDResource is created
but its `status` stays empty. Agent pods crash-loop (`RESTARTS > 0`) and their
logs show a `Failed to watch` / "field label not supported" / "Could not wait
for Cache to sync" cluster of errors — i.e. an informer can't list because a
field selector references a field that the deployed CRD doesn't declare as
selectable. Fix: re-apply CRDs (step 1 above).

Compare quickly:

```bash
diff <(kubectl get crd drbdresourceoperations.storage.deckhouse.io -o yaml \
        | yq '.spec.versions[].selectableFields') \
     <(yq '.spec.versions[].selectableFields' \
        crds/storage.deckhouse.io_drbdresourceoperations.yaml)
```

### Base image kernel/userspace mismatch

Symptom: tests fail fast (sub-second each) with the agent setting
`Configured=False` and a reason from the resource-options / disk-options step;
the message names a `drbdsetup` flag the binary rejects. The kernel module is
the Flant build (so the agent enables Flant-only options) but the `drbdsetup`
inside the base image is upstream and doesn't know them. Fix: bump the
`FROM ...@sha256:` digest in `Dockerfile-dev` and rebuild.

**Do not** "fix" this by changing `images/agent/pkg/drbdutils/caps.go`. Its
kernel-only check is intentional.

#### Picking a base-image digest

The dev base image is the **agent** image at
`dev-registry.deckhouse.io/sys/deckhouse-oss/modules/sds-replicated-volume`.
Old digests get GC'd, so digests from `git log` are not reliably pullable; the
reliable source is the cluster's currently-installed module.

The catch: this runbook overwrites the daemonset's `agent` container with your
dev tag, so reading the live `agent` container does **not** give you the
production digest. Take it from the most recent Deckhouse-managed daemonset
revision instead (the digest the module last deployed, still pullable):

```bash
kubectl -n d8-sds-replicated-volume get controllerrevisions -o json \
  | jq -r '[.items[] | .data.spec.template.spec.containers[]?
            | select(.name=="agent" and (.image | contains("dev-registry")))
            | .image] | last'
```

(If Deckhouse has not been scaled down yet, you can instead read the live
`agent` image _before_ the first override — same digest.)

Verify the base before committing the change. The image is distroless (no
`/bin/sh`), so run binaries directly — do not wrap in `sh -c`:

```bash
docker login dev-registry.deckhouse.io  # creds in d8-system/deckhouse-registry
IMG=dev-registry.deckhouse.io/sys/deckhouse-oss/modules/sds-replicated-volume@sha256:<DIGEST>

# drbdsetup is the Flant build (lists Flant-only options the agent passes):
docker run --rm --entrypoint=/sbin/drbdsetup "$IMG" help resource-options

# dmsetup must be present (the agent shells out to it):
docker export "$(docker create "$IMG" /agent)" \
  | tar -tf - | grep -E 'sbin/(dmsetup|drbdsetup|drbdadm|drbdmeta)$'
```

The first output should list any Flant-only options the agent currently passes
(currently `--quorum-dynamic-voters`, possibly more in the future — diff
against `images/agent/pkg/drbdutils/` if unsure). The second must list both
`dmsetup` and the drbd binaries.

Registry creds:

```bash
kubectl -n d8-system get secret deckhouse-registry \
  -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d
```

## Triage on failure

1. Find the **first** failure in the test log; later ones tend to cascade.
2. `kubectl -n d8-sds-replicated-volume get pods -l app=agent` — non-zero
   `RESTARTS` after a redeploy means cache-sync failure (almost always CRD
   drift; do not chase test logic until that's fixed).
3. `kubectl -n d8-sds-replicated-volume logs ds/agent -c agent --tail=2000`,
   filter by the failing object's name and by `error`/`ERROR`.
4. Check leftover state — anything `e2e-*` left behind is a cleanup-path bug.
   `upg-*` leftovers from old runs are unrelated and can be ignored:

   ```bash
   kubectl get drbdresource,drbdmapper,drbdrop -A | grep -v upg-
   kubectl get llv -o json | jq '.items[]
     | select(.metadata.name | startswith("e2e-"))
     | {name: .metadata.name, finalizers: .metadata.finalizers}'
   ```

## Verifying a clean run

After green:

- `kubectl get drbdresource,drbdmapper,drbdrop -A | grep -v upg-` — empty.
- `kubectl get llv | grep '^e2e-'` — empty.
- `kubectl -n d8-sds-replicated-volume get pods -l app=agent` — all
  `RESTARTS == 0`.
