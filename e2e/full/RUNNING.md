# Running the Full E2E Suite

Runbook for executing `e2e/full` against a remote cluster reachable via
`kubectl`, to verify a local change to `images/controller` and/or
`images/agent`. See [`TEST_CASES.md`](TEST_CASES.md) (and the per-status
`TEST_CASES_*.md`) for *what* the suite covers.

This suite drives the new control plane (controller + agent) end-to-end:
RV/RVR/RVA orchestration, RSC/RSP lifecycle, datamesh formation, and
migration from the previous control plane. It is therefore stricter on
cluster shape than `e2e/agent`.

## Ground rules

- The suite is expected to pass on `HEAD`. Treat any failure as a regression.
- The suite cleans up after every spec via `DeferCleanup`. Anything `e2e-*`
  left behind after a green run is a real cleanup-path bug — capture state
  and report before forcing cleanup.
- **Never strip a finalizer manually during a healthy run.** The agent /
  controller must release their own finalizers; bypassing that hides
  cleanup-path bugs. Forced cleanup is only acceptable after a
  framework-side timeout that we already understand (see "Forced
  cleanup" below).
- Don't assume the cluster matches `HEAD`. CRD drift, controller and
  agent image drift vs. local `git HEAD`, and base-image kernel/userspace
  drift are the most common causes of failure from a fresh build.
- Migration tests (`Upgrade`-labeled) are I/O-heavy and need exclusive
  access to the diskful nodes during their `EmulatePreexisting` step.
  See "Parallelism" below — high parallelism on a small cluster causes
  widespread spec timeouts that look like product bugs but are
  cluster-saturation cascades.

## Cluster preconditions

In addition to everything `e2e/agent` requires:

1. **At least 4 worker nodes** labelled
   `storage.deckhouse.io/sds-replicated-volume-node=`. Tests with
   `Req:MinNodes:4:1:LVMThin` need 4 diskful + 1 extra; the master
   typically serves as the extra/diskless node and counts toward
   `extra` only.
2. **LVMVolumeGroups configured for thin provisioning.** Every worker
   LVG must declare a thin pool. The suite defaults to `LVMThin` for
   most layouts; without thin pools, every test that requires
   `LVMThin` is silently skipped.
3. **Two `ReplicatedStoragePool`s** must exist:
   - `e2e-thin` (`type: LVMThin`) covering all worker LVGs and naming
     each LVG's thin pool.
   - `e2e-thick` (`type: LVM`) covering the same LVGs, no thin pool.
   - Both names are overridable via `E2E_RSP_THIN` / `E2E_RSP_THICK`.
4. **Custom `:$USER-controller` and `:$USER-agent` images** rolled into
   the cluster, since the suite tests *your* HEAD, not the module's
   shipped image.
5. **Deckhouse scaled to 0** (or this run will lose to its
   reconcile loop).

## Configuration

`e2e/full` reads no `.env.json`; cluster shape is discovered at runtime
via the framework's `Discovery` step (RSPs, nodes, LVGs). Behaviour is
controlled by environment variables:

| Variable                  | Effect                                                              | Default      |
| ------------------------- | ------------------------------------------------------------------- | ------------ |
| `E2E_RSP_THIN`            | RSP name to use as the thin pool                                    | `e2e-thin`   |
| `E2E_RSP_THICK`           | RSP name to use as the thick pool                                   | `e2e-thick`  |
| `E2E_TIMEOUT_MULTIPLIER`  | Multiply all `SpecTimeout` and `Eventually` budgets (e.g. `2.0`)    | `1.0`        |
| `E2E_ALLOW_DISRUPTIVE`    | Set to `true` to run `Disruptive` specs; otherwise auto-skipped     | unset        |
| `E2E_FLAKE_ATTEMPTS`      | `--flake-attempts=N` for `hack/run-e2e-new.sh`                      | `1`          |
| `E2E_SUITE`               | Selects sub-suite for `hack/run-e2e-new.sh` (`full`, `agent`, …)    | `control-plane` |

## Run it

These steps assume `$USER` is unique enough to namespace your dev image
tag. Run from the repo root.

```bash
# 1) Apply the repo's CRDs to the cluster (asks for user confirmation).
#    Required whenever the API package changed since the cluster's module
#    version. Idempotent.
kubectl apply -f crds/storage.deckhouse.io_*.yaml

# 2) Build, push and roll the controller. Image lands at
#    registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-controller
docker build \
  -t registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-controller \
  -f images/controller/dev/Dockerfile-dev .
docker push registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-controller
kubectl -n d8-sds-replicated-volume patch deployment controller --type=json \
  -p='[
    {"op":"replace","path":"/spec/template/spec/containers/0/image",
     "value":"registry.flant.com/deckhouse/storage/sds-replicated-volume:'$USER'-controller"},
    {"op":"add","path":"/spec/template/spec/containers/0/imagePullPolicy",
     "value":"Always"},
    {"op":"add","path":"/spec/template/spec/imagePullSecrets",
     "value":[{"name":"fox-registry"}]}
  ]'
kubectl -n d8-sds-replicated-volume rollout restart deployment controller
kubectl -n d8-sds-replicated-volume rollout status  deployment controller --timeout=180s

# 3) Build, push and roll the agent — same recipe as e2e/agent.
#    See e2e/agent/RUNNING.md for Dockerfile-dev / digest selection.
docker build \
  -t registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-agent \
  -f images/agent/dev/Dockerfile-dev .
docker push registry.flant.com/deckhouse/storage/sds-replicated-volume:$USER-agent
kubectl -n d8-sds-replicated-volume rollout restart ds agent
kubectl -n d8-sds-replicated-volume rollout status  ds agent --timeout=180s

# 4) Make sure Deckhouse is not reverting your changes.
kubectl -n d8-system scale deploy deckhouse --replicas=0

# 5) Provision the thin pools (one per worker LVG) and the two RSPs.
#    Adapt names to your cluster.
for lvg in lvg-0-rv-redos-0 lvg-0-rv-redos-1 lvg-0-rv-ubuntu-0 lvg-0-rv-ubuntu-1; do
  kubectl patch lvmvolumegroup "$lvg" --type=merge \
    -p '{"spec":{"thinPools":[{"name":"thin-0","size":"10Gi"}]}}'
done
# Wait for thinPoolReady=1/1 on each LVG before creating e2e-thin.

cat <<'YAML' | kubectl apply -f -
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStoragePool
metadata:
  name: e2e-thin
spec:
  type: LVMThin
  lvmVolumeGroups:
    - { name: lvg-0-rv-redos-0,  thinPoolName: thin-0 }
    - { name: lvg-0-rv-redos-1,  thinPoolName: thin-0 }
    - { name: lvg-0-rv-ubuntu-0, thinPoolName: thin-0 }
    - { name: lvg-0-rv-ubuntu-1, thinPoolName: thin-0 }
  systemNetworkNames: [Internal]
---
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStoragePool
metadata:
  name: e2e-thick
spec:
  type: LVM
  lvmVolumeGroups:
    - { name: lvg-0-rv-redos-0 }
    - { name: lvg-0-rv-redos-1 }
    - { name: lvg-0-rv-ubuntu-0 }
    - { name: lvg-0-rv-ubuntu-1 }
  systemNetworkNames: [Internal]
YAML
# Both RSPs must reach Phase=Ready before running specs.
```

## Run the suite

The convenience wrapper:

```bash
# E2E_SUITE=full picks ./e2e/full/. Presets: smoke|fast|safe|all|self-tests.
E2E_SUITE=full bash hack/run-e2e-new.sh smoke
```

Or call `ginkgo` directly when you need finer control (label filter,
parallelism, timeout):

```bash
( cd e2e/full && \
  ginkgo --procs=1 --label-filter='Upgrade && !/^Bug:/' \
         --timeout=120m -v -r ./ ) | tee /tmp/run.log
```

If `ginkgo` is not on `PATH`, install the version pinned in `go.mod`:

```bash
go install github.com/onsi/ginkgo/v2/ginkgo@$(grep onsi/ginkgo/v2 e2e/full/go.mod | awk '{print $2}')
```

## Labels and presets

The suite uses Ginkgo labels heavily. Key ones:

| Label                                  | Meaning                                                                  |
| -------------------------------------- | ------------------------------------------------------------------------ |
| `Smoke`                                | Minimal sanity set (~1 spec).                                            |
| `Slow`                                 | Long-running specs.                                                      |
| `Upgrade`                              | Migration from v0 (linstor) control plane to v1 (datamesh).              |
| `Disruptive`                           | Destructive actions; auto-skipped unless `E2E_ALLOW_DISRUPTIVE=true`.    |
| `Bug:<short-tag>`                      | Known bug; excluded by suite default `LabelFilter = "!/^Bug:/"`.         |
| `Req:MinNodes:<diskful>:<extra>:<pool>`| Runtime requirement; auto-skipped if cluster doesn't satisfy it.         |
| `Req:ControlPlane:New`                 | Only on new (controller-based) control plane.                            |

`hack/run-e2e-new.sh` presets map to:

- `smoke` — `Smoke`
- `fast` — `Smoke || Full` (currently equivalent to `smoke`; no spec carries `Full`)
- `safe` — `!Disruptive`
- `all` — empty filter; the suite default `!/^Bug:/` applies

## Parallelism

`hack/run-e2e-new.sh` uses `--procs=$(min(NODES, 5))`. **For migration
tests on a 4-thin-pool cluster this is too aggressive.**

`Upgrade`-labeled specs go through `EmulatePreexisting`, which renames
DRBD resources to `sdsrv-*` and tears down the K8s wrappers. Concurrent
specs collide on:

- diskful node capacity (e.g. `MinNodes:3:0` with 4 thin-pool nodes
  leaves only 1 spare; two such specs in parallel both starve);
- DRBD ports (an `sdsrv-*` orphan from a timed-out spec holds the
  port → the next spec hits `errno 102/103`
  `address already in use` on `drbdsetup new-path`);
- LV exclusive-open (a stuck `sdsrv-*` DRBD device blocks
  `drbdmeta dump-md` on the next spec → "Device or resource busy").

Recommended:

- `--procs=1` for any run that includes `Upgrade`-labeled specs.
- `--procs=2` for the rest if cluster has ≥ 4 worker nodes.
- The `safe`/`all` preset's auto-parallelism is fine for non-migration
  suites only.

A single failing migration spec under high parallelism cascades into
dozens of timeouts as orphan ports and LVs accumulate; serial runs
finish faster overall.

## Drift to check before suspecting code

Same as `e2e/agent` (CRDs, builder Go version, base image), plus:

### Controller image drift

Symptom: `EmulatePreexisting` hangs at the 2-minute boundary, or
adoption never completes. Check that the controller pod's image
SHA matches a build of `HEAD`:

```bash
kubectl -n d8-sds-replicated-volume describe deploy controller \
  | grep -E 'Image:|Image ID:'
git log -1 --format=%H
```

If the deployed image was built from an earlier commit, rebuild
(step 2 above).

### Thin pools missing

Symptom: every `LVMThin`-requiring spec is `SKIP`'d at requirements
enforcement, leaving "Skipped: 31" in the summary. Check:

```bash
kubectl get lvmvolumegroup -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.thinPools[*].ready}{"\n"}{end}'
```

Each LVG should report `1/1`. If it reports `0/0`, patch the LVG (step 5).

### RSPs not Ready

Symptom: `[Discovery]` fails the suite at `BeforeSuite` with "RSP not
found" or "RSP not Ready". Check:

```bash
kubectl get rsp e2e-thin e2e-thick
```

Both must be `Phase=Ready`. If they're stuck `Provisioning`, the RSP
controller can't reach an LVG — check LVG node labels and thin pool
status.

## Triage on failure

1. Find the **first** failure in the test log; later ones tend to cascade.
2. `kubectl -n d8-sds-replicated-volume get pods` — non-zero
   `RESTARTS` on `controller` or `agent` after redeploy means
   cache-sync failure (almost always CRD drift).
3. `kubectl -n d8-sds-replicated-volume logs deploy/controller --tail=2000`
   and `logs ds/agent -c agent --tail=2000`, filtered by the failing
   object's name and by `error`/`ERROR`.
4. Inspect the live cluster while the spec is hung (Ginkgo's spec
   timeout is your friend here — you have 2-3 minutes to grab state):

   ```bash
   # Anything still around belongs to in-flight or stuck specs:
   for k in rv rvr rva drbdresource llv rsc; do
     kubectl get "$k" -o json | jq -r --arg k "$k" \
       '.items[] | select(.metadata.name | startswith("e2e-"))
        | "\($k) \(.metadata.name) deletion=\(.metadata.deletionTimestamp // "no") finalizers=\(.metadata.finalizers // [])"'
   done
   # On each node, listing kernel-side DRBD resources:
   for node in $(kubectl get nodes -l storage.deckhouse.io/sds-replicated-volume-node -o name | sed 's|node/||'); do
     pod=$(kubectl -n d8-sds-replicated-volume get pods -l app=agent \
       --field-selector spec.nodeName=$node -o jsonpath='{.items[0].metadata.name}')
     [ -z "$pod" ] && continue
     echo "--- $node ---"
     kubectl -n d8-sds-replicated-volume exec -c agent "$pod" -- \
       drbdsetup status 2>/dev/null | grep -E '^(sdsrv-|e2e-)' || true
   done
   ```

5. Suspicious patterns and what they mean:

   | Pattern                                                                              | Likely cause                                                              |
   | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
   | DRBDR with `deletionTimestamp` + `maintenance=NoResourceReconciliation` + agent finalizer | Framework race when stripping finalizers (agent updated status concurrently). Fixed in `t_rv_emulate_preexisting.go` to retry on conflict; if it recurs, look for new status writers. |
   | Spec times out at exactly 2:00, 3:00 or 4:00                                          | `SpecTimeout` budget exhausted; correlate with parallelism (see above).   |
   | `drbdsetup new-path … (102) Local` / `(103) Remote address already in use`            | An `sdsrv-*` orphan from a previous spec holds the port. Run forced cleanup. |
   | `drbdmeta dump-md … Device or resource busy`                                          | LV held by an `sdsrv-*` orphan DRBD device. Same forced cleanup.          |
   | All `LVMThin` specs skipped                                                           | Thin pools not configured on LVGs (drift section above).                  |
   | `Bug:<tag>` specs auto-excluded                                                       | Expected — that's the suite default `LabelFilter`.                        |

## Verifying a clean run

After green:

- `kubectl get rv,rvr,rva,drbdresource,llv,rsc -o name | grep '/e2e-'` — empty.
- `kubectl -n d8-sds-replicated-volume get pods` — controller and agent
  pods all `RESTARTS == 0`.
- On every node, `drbdsetup status | grep -E '^(sdsrv-|e2e-)'` — empty.

## Forced cleanup

The framework's `SynchronizedAfterSuite` has a 60-second `NodeTimeout`
budget. After very large runs (e.g. all 91 `Upgrade`-labeled specs),
that budget can be exhausted while finalizer-driven deletions are
still draining. The result is leftover `e2e-*` objects despite a green
suite. This is a known limitation of the post-suite scavenger, not a
controller/agent bug.

When you've already accepted that the run was healthy and just need
the cluster ready for the next one:

```bash
# 1. Clear maintenance on stuck DRBDRs (the CRD enum forbids "" so use
#    JSON op:remove, not a merge patch).
for d in $(kubectl get drbdresource -o name | grep '/e2e-'); do
  kubectl patch "$d" --type=json -p='[{"op":"remove","path":"/spec/maintenance"}]' || true
done

# 2. drbdsetup down kernel-side orphans on every node.
for node in $(kubectl get nodes -l storage.deckhouse.io/sds-replicated-volume-node -o name | sed 's|node/||'); do
  pod=$(kubectl -n d8-sds-replicated-volume get pods -l app=agent \
    --field-selector spec.nodeName=$node -o jsonpath='{.items[0].metadata.name}')
  [ -z "$pod" ] && continue
  for r in $(kubectl -n d8-sds-replicated-volume exec -c agent "$pod" -- drbdsetup status 2>/dev/null | grep -oE '^(sdsrv-|e2e-)[a-z0-9-]+'); do
    kubectl -n d8-sds-replicated-volume exec -c agent "$pod" -- drbdsetup down "$r" || true
  done
done

# 3. Strip finalizers from the leftover K8s objects so GC can complete.
for o in $(kubectl get rv,rvr,rva,drbdresource,llv,rsc -o name | grep '/e2e-'); do
  kubectl patch "$o" --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' || true
done
```

If you find leftovers after a *failed* spec — that's a bug. Capture
state, not cleanup.
