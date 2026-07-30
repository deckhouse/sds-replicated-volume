# E2E Module Upgrade Test Cases

End-to-end coverage of a module upgrade performed on a running cluster while its
volumes are under continuous I/O: the module is installed at one image tag,
filled with r3 volumes each carrying a writer pod, retagged to a second image
tag, and finally migrated from r3 to r2 by an in-place edit of
`rsc.spec.replication`.

The suite is **optional** and compares an arbitrary pair of builds — today
`main` → a PR build, later any two. Consequences that shape every case below:

- Everything asserted **before** the retag uses only API both versions publish:
  the datamesh members and their types, the replica `Ready` condition. Neither
  `status.membershipLayout` nor the `MembershipLayoutConverged` condition is read
  there — the older build may predate the rename of that condition.
- The result of the migration is asserted by **counting member types** on one
  snapshot together with the layout condition, not by the layout string alone.

The three cases are the three phases of ONE scenario, in an `Ordered` container
that runs them in this order against one stand and one set of volumes. They share
the setup described below and cannot be run individually.

Each case is titled with the text of its Ginkgo `It` verbatim; the line right
below the title carries the case identifier, its position in this document, the
opt-in class mark, and the `Describe` container the spec lives in.

Run against a real cluster (`e2e/upgrade`, Ginkgo). These specs are written and
compiled here; the actual run happens on the stand — see `README.md` for the
environment contract and the mandatory `--procs=1 --timeout=180m`. Non-obvious
assertions are marked with ⚡.

---

## Shared setup (`BeforeAll`, once for all three cases)

Before the first spec, and outside them:

- The module is installed at `E2E_UPGRADE_FROM_TAG` — a `ModuleConfig` with
  `spec.enabled=true` plus a `ModulePullOverride` with that tag — and the setup
  waits until every workload of the module in `d8-sds-replicated-volume` is
  rollout-complete and ready. This runs as the framework's **pre-discovery
  hook**, before the pools are discovered: discovery reads objects that only
  exist once the module is installed.
- The stand is required to offer at least 3 usable diskful nodes in the selected
  pool (one per replica of an r3 volume). Too few is a **failure**, never a skip.
- A test namespace and a dedicated `ReplicatedStorageClass` are created:
  `replication: ConsistencyAndAvailability` (r3), `topology: Ignored`,
  `reclaimPolicy: Delete`, over the pool's volume groups. `replication` and not
  `failuresToTolerate`/`guaranteedMinimumDataRedundancy`, because the CRD forbids
  holding both and case 3 migrates by editing exactly that field.
- The number of volumes is computed from the pool's free space (70% of
  `status.vgFree` for a thick pool, of the thin pool's `availableSpace` for a thin
  one), spread over the usable diskful nodes, clamped to 5…20 — or taken from
  `E2E_UPGRADE_VOLUMES`. A pool that cannot host 5 volumes fails the run.
- For each volume: a `PersistentVolumeClaim` of that storage class and a pod
  mounting it, writing beat records to a journal **on the volume** with an fsync
  per record, plus a one-shot data file whose sha256 is recorded next to it. The
  setup waits for each pod to run, for the claim to bind, and for the writer's
  first verified writes.
- ⚡ Each volume is tied to its `ReplicatedVolume` through
  `pvc.spec.volumeName`, and `pv.spec.csi.volumeHandle` is asserted to equal the
  PV's own name — so a change in the CSI naming scheme is reported as a naming
  change instead of as a missing object.

Everything above is created in the `BeforeAll` itself so that its cleanups are
`CleanupAfterAll`: the writers must outlive all three cases. Teardown removes the
pods and claims (which takes the PVs and the ReplicatedVolumes with them —
`reclaimPolicy: Delete`), then the storage class and the namespace. The module is
deliberately left at the tag the run ended on.

---

## keeps every r3 volume healthy and writing while the module runs the old version

E2E-UPG-1 · case 1 · Disruptive · Describe: `Module upgrade: r3 volumes under load survive a module retag and an r3->r2 migration`

**The stand prepared by the setup is a valid starting point: every volume is a
healthy 3D volume whose data path is moving.**

Given: the shared setup above, on `E2E_UPGRADE_FROM_TAG`.

Then:
- Every writer pod is ready on its bound claim, has verified at least one write,
  is not stalled now and has not stalled earlier in the run.
- Every volume reports its formation transition complete and a datamesh of
  exactly 3 members, all of type `Diskful`.
- Every replica of every volume reports `Ready=True`. ⚡ Asserted by condition
  STATUS and not by reason: a diskful replica is `Ready/Ready` while a
  tie-breaker is `Ready/QuorumViaPeers`, and the reason set is exactly the kind of
  detail that may differ between the two builds being compared.
- Every writer completes 5 more verified writes, so the claim "the data path
  works" is about the data path and not about conditions.
- Every data file re-hashes to the digest recorded when it was written — the
  pre-upgrade checksum baseline.

---

## retags the module to the new version without freezing the I/O of any volume

E2E-UPG-2 · case 2 · ⚡ Disruptive · Describe: `Module upgrade: r3 volumes under load survive a module retag and an r3->r2 migration`

**Retagging the live `ModulePullOverride` rolls every workload of the module over
without stopping the I/O of the volumes it serves.**

Given: case 1 passed; every volume is 3D and under load.

When: the `ModulePullOverride` is retagged to `E2E_UPGRADE_TO_TAG` and the run
waits until the module runs that build — the published bundle digest changed and
every workload (controller, csi-controller, spaas, webhooks, agent, csi-node) is
rollout-complete with its pods ready.

Then:
- Every writer completes 5 more verified writes after the rollout, so each
  volume's data path is alive on the new version.
- ⚡ No volume froze longer than `E2E_UPGRADE_MAX_IO_FREEZE` (default 30s) at any
  point since the writer started. Measured over the **whole** journal, not its
  tail: the freeze this case exists for is caused by the agents restarting, i.e.
  it is a gap that already ENDED by the time anything looks at it, and a tail read
  would show a happily beating writer instead. The report names the longest gap
  and every gap over the tolerance, with beat numbers and timestamps.
- Every volume is still a datamesh of 3 diskful members, and every replica is
  `Ready` again — the upgrade changed the code, not the layout.

---

## migrates every volume to 2D+1TB on the new version with its data intact

E2E-UPG-3 · case 3 · ⚡ Disruptive · Describe: `Module upgrade: r3 volumes under load survive a module retag and an r3->r2 migration`

**A single edit of `rsc.spec.replication` on the upgraded module migrates every
volume of the class from 3D to 2D+1TB, under load, without losing a byte.**

Given: case 2 passed; the module runs `E2E_UPGRADE_TO_TAG` and every volume is a
3D volume under load, created while the module still ran the old build.

When: `rsc.spec.replication` is edited from `ConsistencyAndAvailability` to
`Availability` — once, on the storage class, which is the migration trigger.
`rv.spec.replicatedStorageClassName` is never touched.

Then:
- ⚡ Every volume converges to `MembershipLayoutConverged=True/Converged` with a
  datamesh of exactly 2 `Diskful` members and 1 `TieBreaker`. Both halves are
  evaluated on ONE snapshot: the condition alone was already true of the
  pre-migration 3D volume, and the composition alone can be reached before the
  transition reports itself complete.
- Every remaining replica reports `Ready=True`.
- Every writer completes 5 more verified writes, and no volume froze longer than
  the tolerance — again over the whole journal, so a freeze during the retype is
  caught after the fact.
- ⚡ Every data file still hashes to the digest recorded **before the upgrade**,
  re-read inside the very pod that wrote it (the claim is ReadWriteOnce, so no
  second reader could mount it). This is the end-to-end statement of the suite:
  the bytes written on the old version, through the old CSI path, are intact
  after an upgrade and a layout migration.
