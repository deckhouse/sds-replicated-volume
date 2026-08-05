# Running the Module Upgrade E2E Suite

`e2e/upgrade` is an **optional** Ginkgo suite that upgrades the module *under
load* on an already running cluster and proves the volumes survive it. One
scenario, three phases, one `Ordered` container:

1. the module is installed at `E2E_UPGRADE_FROM_TAG`, a storage class with
   `replication: ConsistencyAndAvailability` (r3) is created and filled with
   volumes, each with a pod writing to it continuously;
2. the `ModulePullOverride` is retagged to `E2E_UPGRADE_TO_TAG` and every
   workload of the module is rolled out under that load;
3. `rsc.spec.replication` is edited to `Availability`, which migrates every
   volume from 3 diskful replicas to 2 diskful + 1 tie-breaker.

Throughout, each volume's writer keeps a journal **on the volume** and a data
file whose sha256 was recorded when it was written. That is what turns "the
upgrade worked" into two checkable statements: the I/O never stopped for longer
than the tolerance (measured over the whole journal, so a freeze that already
ended still counts), and the data is byte-identical afterwards.

The suite does not know which two builds it compares. Everything it asserts
*before* the retag is therefore built out of API both versions publish — the
datamesh members and their types, the replica `Ready` condition — never out of
fields or condition names one of them may not have yet.

## Ground rules

- **The run is destructive and cluster-wide.** Retagging the module restarts
  every one of its workloads, for every workload on the cluster, not only for
  this suite's volumes. Do not point it at a stand somebody else is using.
- **The suite never skips a spec.** A stand too small for the scenario, a pool
  with too little free space, an unreadable volume group — all of them fail the
  run. A silent skip would hide a degraded stand behind a green summary.
- **After a successful run the cluster stays on `E2E_UPGRADE_TO_TAG`**, with the
  test namespace, the storage class and the volumes removed. After a FAILED run
  the cluster is left exactly as the failure found it — including "still on
  `E2E_UPGRADE_FROM_TAG`", or halfway through a rollout. That state is the
  diagnosis material, so nothing retags it back; restore it yourself when you are
  done looking.
- The `e2e/full` suite is untouched by all this and must not be run at the same
  time against the same cluster.

## Configuration

| Variable | Required | Meaning | Default |
| --- | --- | --- | --- |
| `E2E_UPGRADE_FROM_TAG` | **yes** | `spec.imageTag` the module is installed at before anything is created (the OLD build) | — (suite skipped) |
| `E2E_UPGRADE_TO_TAG` | **yes** | `spec.imageTag` the module is retagged to in phase B (the NEW build) | — (suite skipped) |
| `E2E_ALLOW_DISRUPTIVE` | **yes** (`true`) | Enables the `Disruptive` class every spec of this suite belongs to. `E2E_RUN_ALL=true` does the same | unset (suite fails) |
| `E2E_UPGRADE_VOLUMES` | no | Number of volumes, overriding the computation; a decimal integer ≥ 1. Above 20 raise `--timeout` (see below) | computed from the pool's free space, clamped to 5…20 |
| `E2E_UPGRADE_VOLUME_SIZE` | no | Size of one volume, a Kubernetes quantity | `1Gi` |
| `E2E_UPGRADE_POOL_TYPE` | no | Which discovered pool to use: `thin` or `thick` | `thin` |
| `E2E_UPGRADE_MAX_IO_FREEZE` | no | Longest tolerated I/O freeze, a Go duration | `30s` |
| `E2E_UPGRADE_IMAGE` | no | Image of the writer pod; needs a POSIX `sh`, `sha256sum` and `date` | `busybox:latest` |
| `E2E_RSP_THIN` / `E2E_RSP_THICK` | no | Names of the stand's `ReplicatedStoragePool`s, as in `e2e/full` | `e2e-thin` / `e2e-thick` |
| `E2E_TIMEOUT_MULTIPLIER` | no | Framework multiplier — scales the phases' `SpecTimeout` and **nothing else** (see below) | `1` |

Every one of these is read and validated **once**, in `TestUpgrade`, before
Ginkgo starts. Two outcomes:

- without the two tags the suite is **skipped** as a whole — it is optional and
  has nothing to compare;
- with a value it cannot use it **fails immediately**: two identical tags (an
  upgrade that upgrades nothing), a tag that is empty or has spaces after
  quoting, a volume count that is not a number, a pool type that is neither
  `thin` nor `thick`, or `E2E_ALLOW_DISRUPTIVE` left off.

That last check is not redundant with the framework's class gate. The gate skips
specs from `JustBeforeEach`, which Ginkgo reaches only **after** `BeforeAll` — so
without this check the suite would install the module, create every volume with a
pod, and only then report all three specs as skipped, exiting 0. Note also that
the variable is parsed as a **boolean**: `true`, `1`, `t` (any case) enable the
class; `yes`, `on` and anything else do not.

### The writer image

The default `busybox:latest` comes from Docker Hub. If the stand cannot reach it
— or simply has not cached it — every writer pod ends up in `ImagePullBackOff`
and the setup fails naming that image and the container's waiting reason. The fix
is not to make the stand reach Docker Hub, it is to name an image it already has:

```bash
export E2E_UPGRADE_IMAGE=registry.example.internal/library/busybox:1.36
```

Any busybox-compatible image works; the writer needs nothing beyond a POSIX
shell, `sha256sum`, `date` and the usual text tools. The pods are created with
`imagePullPolicy: IfNotPresent`, so a cached image is enough.

## Run it

Run `ginkgo` **directly**, always with `--procs=1` and an explicit `--timeout`:

```bash
export E2E_UPGRADE_FROM_TAG=main
export E2E_UPGRADE_TO_TAG=pr758
export E2E_ALLOW_DISRUPTIVE=true

( cd e2e/upgrade && ginkgo --procs=1 --timeout=180m -v -r ./ ) | tee /tmp/upgrade.log
```

with an internal writer image:

```bash
export E2E_UPGRADE_IMAGE=registry.example.internal/library/busybox:1.36
( cd e2e/upgrade && ginkgo --procs=1 --timeout=180m -v -r ./ ) | tee /tmp/upgrade.log
```

**`hack/run-e2e-new.sh` does not work for this suite.** It derives `--procs` from
the number of storage nodes — on a four-node stand that is four workers, each
running the pre-discovery hook and installing the module — and it never passes
`--timeout` at all.

- `--procs=1` is not only about cost. The scenario is a single `Ordered`, `Serial`
  container, so parallelism buys nothing, and with one process both halves of
  `SynchronizedBeforeSuite` run in it — which is what makes the module
  installation happen exactly once.
- `--timeout` is mandatory because Ginkgo's default is **1h** and the scenario
  does not fit in it. Without it the run is cut off mid-phase and leaves the
  cluster on an intermediate version.

### Choosing `--timeout`

The suite budgets its nodes for `N` volumes, where `N` is `E2E_UPGRADE_VOLUMES`
when set and the ceiling of the clamp (20) otherwise:

```
--timeout ≥ 15m                  module install (pre-discovery hook)
         + (10m + 1m×N)          setup: claims, pods, first writes
         + ( 5m + 30s×N)         phase A
         + 20m                   phase B (the retag)
         + (10m + 2m×N)          phase C (the migration)
         + 15m                   cleanup reserve
```

which is 93m at N=5 (rounded up from 92m30s) and 145m at N=20 — hence the default of
**`--timeout=180m`**. With `E2E_UPGRADE_VOLUMES` above 20, recompute it. The
cleanup reserve is not optional padding: the cleanups that stop the writers and
delete the claims have no timeout of their own and are bounded only by the suite
timeout.

### A slow stand

`E2E_TIMEOUT_MULTIPLIER` scales the `SpecTimeout` of the three phases and
**nothing else** — not the `NodeTimeout` of the setup, not the readiness budget
of the module install, not the suite timeout. On a stand where the module needs
longer to roll out or the volumes longer to converge, raise:

- `moduleReadyBudget` in `suite_test.go` (the readiness budget of both module
  operations),
- the budget constants at the top of the container in `module_upgrade_test.go`,
- and `--timeout` accordingly.

## What the phases assert

See `TEST_CASES_UPGRADE.md` for the full list, one entry per spec, titled with
the spec's text verbatim.

## Triage on failure

- **`ImagePullBackOff` on a writer pod** — the setup fails naming the image and
  the container's waiting reason. Set `E2E_UPGRADE_IMAGE` (see above).
- **The module never becomes ready** — the failure names the earliest link of the
  retag chain that has not happened yet, so read it literally:
  - *no `status.imageDigest`* or *still the digest it had before the retag* —
    Deckhouse has not resolved the tag to a bundle. Check that the tag exists in
    the dev registry and that Deckhouse is not scaled to zero.
  - *`Module` reports `properties.version` X, not the pinned tag* — the bundle is
    there, but the Deckhouse that will re-apply the manifests has not restarted
    into it yet. Writing the digest makes Deckhouse restart itself, and it is
    that restart which restores the module from the pinned tag; a minute here is
    normal, longer means the restart is stuck (look at `d8-system`).
  - *`IsReady` still carries the transition it had before the write* — Deckhouse
    is up on the new bundle but has not re-run the module, so the `Ready` phase
    still readable at that moment is the STALE one from before the retag. This is
    the gate that keeps the suite from measuring the old build.
  - *`Module` is in phase X, not `Ready`* or a workload that did not roll out —
    the module is being re-applied right now; the message names the workload and
    its counters.
  - *held for N of M polls* — everything was accepted, then something moved
    again. A second re-apply landed inside the observation window; it only fails
    if it keeps happening for the whole budget.
- **A freeze report** — the message carries the longest gap, every gap over the
  tolerance with its beat numbers and timestamps, and the path of the journal
  inside the pod. The journal survives as long as the pod does, which after a
  failure is until you remove the namespace.
- **A volume that did not converge** — the matcher message carries the whole
  composition of the datamesh (member, type, node) plus the layout string and the
  `MembershipLayoutConverged` condition, on one snapshot.

## A note on the future

The module install and the retag live in `e2e/pkg/framework/module_lifecycle.go`
because nothing else in this repository installs the module today. When an
e2e cluster bootstrap lands (the work that brings up a cluster from scratch), its
module installation and this helper should become one; they answer the same
question — "the module is running the build I asked for" — and two answers to
that will drift.
