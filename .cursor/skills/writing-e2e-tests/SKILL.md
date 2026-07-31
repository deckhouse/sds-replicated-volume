---
name: writing-e2e-tests
description: Write and maintain e2e integration tests in both suites — e2e/agent (plain Go + envtesting Setup/Discover helpers, documented in e2e/agent/TESTCASES.md) and e2e/full (Ginkgo specs built on the shared e2e/pkg/framework, documented in e2e/full/TEST_CASES*.md and e2e/full/RUNNING.md) — including framework helpers under e2e/pkg/framework and their unit tests, the boundary between framework helpers and spec-local helpers in _test.go files, and keeping the test-case docs in sync with the Go test code (an entry title is the Ginkgo It text verbatim). Use when editing anything under e2e/, when adding or changing a framework helper, when deciding where a helper belongs, and when planning or reviewing e2e coverage.
---

# Writing e2e tests

The repository has two independent e2e suites. They share nothing but the `e2e/`
root: each has its own Go module, style, helper contract and test-case
documentation.

| Suite | Test code | Style | Test-case docs |
| --- | --- | --- | --- |
| agent | `e2e/agent` | plain Go tests, `envtesting.E`, `SetupX`/`DiscoverX` helpers | `e2e/agent/TESTCASES.md` |
| full | `e2e/full` | Ginkgo/Gomega specs built on `e2e/pkg/framework` | `e2e/full/TEST_CASES*.md`, runbook in `e2e/full/RUNNING.md` |

Read the section for the suite you are editing. `e2e/pkg/framework` belongs to
the `e2e/full` contract.

`hack/run-tests.sh` does NOT cover the e2e modules. Verify them explicitly, from
each module directory:

```bash
cd e2e/pkg/framework && go test ./... && go vet ./...   # helper unit tests, no cluster
cd e2e/full          && go test -run '^$' ./... && go vet ./...   # specs must compile
```

Running the `e2e/full` specs themselves requires a cluster and is done by the
user, never by an agent.

# Suite `e2e/agent`

This section covers writing and maintaining end-to-end tests for the e2e agent.

 - e2e agent test code lives in `e2e/agent`.
 - Every e2e test is composed from **test helpers** — reusable, focused building
   blocks described below.
 - Human-readable test case descriptions are maintained in
   `e2e/agent/TESTCASES.md`.

## Test helpers

A test helper is a Go function that serves as a reusable building block for e2e
tests. Every e2e test MUST be composed from test helpers. Each helper should be
focused on a single responsibility and reusable across tests.

There are two categories of test helpers:

 - **Setup helpers** (`SetupX`) — have at least one Arrange step (and,
   therefore, Cleanup). They create or modify state in the environment and
   provide a service with guarantees about that state. Setup helpers MUST
   discover whether what they are setting up already exists before arranging
   (see Discover step below). For most resources, this means a paired
   `discover_X.go` and `setup_X.go`, where the setup helper calls the discover
   helper.
 - **Discovery helpers** (`DiscoverX`) — discovery-only. They discover and
   validate existing state in the environment (I/O: reading files, environment
   variables, querying a cluster, reading kubeconfig) but do not arrange
   anything and do not need cleanup.

Pure value construction from arguments (no I/O) is not a test helper — use Go's
standard `New*` pattern for that.

All test helpers MUST follow common naming and signature conventions (see
Signature below). Each helper is composed of the following steps (see
corresponding sections below):

 - Require — both categories
 - Discover — both categories
 - Arrange/Act — setup helpers only
 - Cleanup — setup helpers only
 - Assert — both categories
 - Provide — both categories

### Signature

The first argument of every test helper MUST be `e envtesting.E`. `envtesting.E`
is an interface, so it MUST NOT be taken by pointer (`*envtesting.E`).

```go
// SetupX Provides X, which is guaranteed to be ..., can be accessed via ...
func SetupX(e envtesting.E, <REQUIREMENTS>) <PROVIDED_X_SERVICE> {
    // May call any test helpers (Setup, Discover).
}

// DiscoverX Discovers existing X, which is guaranteed to be ...
func DiscoverX(e envtesting.E, <REQUIREMENTS>) <PROVIDED_X_SERVICE> {
    // May call other Discover helpers, but NOT Setup helpers.
}
```

### `t.Helper()` — do NOT use in test helpers

Test helpers MUST NOT call `t.Helper()`. When a test fails inside a helper, the
stack trace must point to the exact line inside the helper where the failure
occurred — not to the caller. `t.Helper()` hides the helper from the stack
trace, making it harder to diagnose failures.

### Error message prefixes

Error messages in test helpers MUST be prefixed with the step name, not the
helper function name. Use lowercase step names matching the helper step
structure: `require:`, `discover:`, `arrange:`, `cleanup:`, `assert:`.

```go
e.Fatal("require: option must not be empty")
```

Error messages that describe what happened (e.g., `"getting LVG %q: %v"`,
`"creating DRBDResource %q: %v"`) do not need a step prefix — the context is
clear from the message itself.

### Require

This is what the test helper requires from the environment to be true. In order
to enforce these requirements, it MAY accept arguments after `e` (compile-time
requirements) and also do its own validation of them at the beginning of the
test helper.

A failed requirement is a reason to fail the test (fatal).

Requirements that are not strictly guaranteed by the Go type system MUST be
validated and MUST be described in the test helper's documentation.

Configuration of test helpers MUST be passed as explicit arguments. Helpers MUST
NOT call `e.Options()` internally — the caller is responsible for discovering
options and passing them to the helper. This keeps each helper's dependencies
visible in its signature.

### Arrange/Act

Applies only to setup helpers.

Arrange is what setup helper does in the environment to set up the state. These
actions MUST be reverted at a Cleanup step (see below).

For example, turning on a feature flag in order to test a feature is an Arrange
step.

Act is a side effect that does not need its own cleanup because reverting the
preceding Arrange is sufficient. For example, exercising a feature after the
flag was turned on is Act.

Failure during Arrange is usually fatal, since the environment will be left
partially initialized. Failure during Act MAY be non-fatal, since it only
indicates something not working, not necessarily critical for the rest of the
test.

Arrange step is where the setup helper is allowed and expected to call other
setup helpers, making it easier to read and reuse the code.

### Cleanup

Applies only to setup helpers.

Setup helpers MAY leave side effects in the system after returning (see
Arrange). But they MUST register cleanup at the test cleanup phase. To achieve
that, setup helpers MUST use `e.Cleanup(func() { /* do cleanup here */ })`.

As always with deferred cleanups, they MUST be deferred right after the Arrange
of the corresponding side effect.

Cleanup failures are not fatal, but should be reported as test failures.

### Assert

Applies to setup and discovery helpers.

Assert is the validation of environment state. This is how guarantees are
provided to the rest of the test.

It may happen right after Arrange/Act, or during Cleanup.

Failed asserts MAY be fatal or non-fatal, depending on what is being asserted.

Important aspects of the state that were asserted MAY be mentioned in the
documentation.

### Provide

This is what the test helper returns to the calling test helper (or the root
test). Usually it is a service encapsulating the provided state. It may then be
passed to other test helpers that require it.

Returning an error from a test helper does not make sense, since errors are
reported as test failures.

Documentation of the test helper should describe what the Go type system cannot
express about the provided service — guarantees, sorting order, etc.

### Discover

Applies to setup helpers and discovery helpers.

**In setup helpers:** before arranging, a setup helper MUST check whether the
desired state already exists and is valid. If it does, Arrange is skipped. If
the discovered state is partially valid and would conflict with a new
arrangement, this MUST be a fatal test failure. A setup helper that supports
Arrange MUST also support Discover, unless the arrangement itself is
idempotent. For most resources, the discover logic SHOULD be extracted into a
separate `DiscoverX` helper so it can be reused independently.

**In discovery helpers:** Discover is the only meaningful step. The helper
discovers and validates existing state, then provides it. If the state is not
found or not valid, this MUST be a fatal test failure. Discovery helpers never
have Arrange or Cleanup steps.

## Root test

The root test is a normal Go test that follows standard Go testing conventions.
The key difference from usual unit tests is that it is an integration test
running against a real environment.

### Hierarchical structure

Arranging state in an environment is costly — we cannot afford doing it for each
test case individually. Instead, tests are structured to reuse the environment
as much as possible. This means a hierarchical test structure using `e.Run` for
subtests, where subtests run sequentially and reuse state defined in the parent
scope.

```go
func TestReplicatedVolume(t *testing.T) {
    e := envtesting.New(t)
    client := DiscoverClient(e)

    // Arrange: set up shared environment state.
    storageClass := SetupStorageClass(e, client)
    // e.Cleanup registered inside SetupStorageClass.

    e.Run("WithSingleVolume", func(e envtesting.E) {
        // Arrange: narrow state for this group of subtests.
        volume := SetupVolume(e, client, storageClass)

        e.Run("VolumeIsAccessible", func(e envtesting.E) {
            // Test case: uses volume from parent scope, no extra arrange.
        })

        e.Run("VolumeCanBeResized", func(e envtesting.E) {
            // Test case: uses volume from parent scope, acts and asserts.
        })
    })

    e.Run("WithReplicatedVolume", func(e envtesting.E) {
        // Arrange: different state, same storageClass from root scope.
        volume := SetupReplicatedVolume(e, client, storageClass)

        e.Run("ReplicationIsHealthy", func(e envtesting.E) {
            // Test case: uses volume from parent scope.
        })
    })
}
```

### Relation to test helpers

In terms of test helpers, the root test behaves like a setup helper without
Require and Provide: it arranges state, registers cleanup, and runs subtests.
Subtests depend on state from the parent scope (Require) but do not provide
anything.

One important difference from setup helpers: in the root test, all arranged
state is cleaned up before the test exits (via `e.Cleanup`), so no state leaks
out.

### Configuration

The config file (`.env.example.json`) is a flat JSON object. Each top-level key
is named after the Go type that the section unmarshals into via
`e.Options(&target)`.

```json
{
  "TestId": "e2e",
  "FooOptions": {
    "option1": "value1"
  },
  "BarOptions": {
    "option2": 42
  }
}
```

Resource names in e2e tests MUST include a deterministic test identifier (the
`TestId` config field). Random or timestamped identifiers MUST NOT be used —
deterministic IDs ensure leftover resources from a failed cleanup are detected
on the next run.

### File organization

Each test helper MUST live in its own file. The file SHOULD be named after the
helper (e.g., `setup_llvs.go` for `SetupLLVs`, `discover_nodes.go` for
`DiscoverNodes`). Closely related private functions (cleanup, wait, internal
types) MAY live in the same file as the helper they support.

## Test cases

Test cases are documented in `e2e/agent/TESTCASES.md`. This file has two
sections: **Preconditions** and **Test cases**.

**Preconditions** are named 1-to-1 with setup helpers. Each precondition
describes the environment state that a setup helper arranges.

**Test cases** are a flat list. Each test case has:
 - A title, which MUST match the Go test name.
 - A list of preconditions required for the test.

Any grouping or structure within `TESTCASES.md` is purely for readability and
has no relation to the actual Go test hierarchy.

### Two views of the same tests

`TESTCASES.md` and the Go test hierarchy serve different purposes:

 - **`TESTCASES.md`** is optimized for **readability**. Test cases are listed
   flat, grouped by topic for humans.
 - **Go test hierarchy** is optimized for **environment reuse**. Tests are
   nested with `e.Run` so that subtests sharing the same preconditions live
   under a common parent that arranges those preconditions once.

These two views MUST be kept in sync: whenever a test case is added, renamed, or
removed in one, the other MUST be updated to match.

### Placing a new test in Go hierarchy

When a test case is added to `TESTCASES.md`, the corresponding Go test MUST NOT
simply be appended at the top level. Instead, find (or create) a place in the
existing `e.Run` hierarchy where all of the test's preconditions are already
arranged by a parent scope. This ensures the environment is reused and avoids
redundant setup.

# Suite `e2e/full`

`e2e/full` drives the new control plane (controller + agent) against a real
cluster with Ginkgo/Gomega specs. All cluster interaction goes through the
shared framework in `e2e/pkg/framework`.

## Suite structure

The suite has exactly one entry point (`e2e/full/suite_test.go`) holding the
package-level framework instance:

```go
var f = fw.Setup()
```

`fw.Setup()` registers the Ginkgo lifecycle hooks (cluster discovery, timeout
policy, label transformers, scavenging). Specs MUST NOT create their own
framework, `BeforeSuite`, or Kubernetes client.

Every spec file declares top-level containers:

```go
var _ = Describe("Layout: r3->r2 migration by editing rsc.spec.replication",
    Label(fw.LabelSlow), Label(fw.LabelFeatureMembership), func() {

        It("migrates a 3D volume to 2D+1TB (one diskful retyped to tie-breaker)",
            SpecTimeout(10*time.Minute), require.MinNodes(3), func(ctx SpecContext) {
                By("creating an r3 storage class and a 3D volume")
                ...
            })
    })
```

Rules:

 - The spec body MUST take `ctx SpecContext` and pass it to every framework
   call; never `context.Background()` and never a bare `context.TODO()`.
 - Each phase of a spec MUST be introduced with `By("...")` describing the
   action in the domain's language.
 - The spec text is the test case title used in `TEST_CASES*.md` (see below).

## Timeouts

The timeout policy is enforced by a Ginkgo transformer (`timeout_policy.go`):
the default `SpecTimeout` is 30s, `Label(fw.LabelSlow)` raises that default to
1min and `Label(fw.LabelLongHaul)` raises it to 30min (LongHaul dominates when
both are present), and an explicit `SpecTimeout` above 30s requires one of
those two labels — with neither, the transformer fails the tree with
`add Label("Slow") or Label("LongHaul"), or reduce the timeout`. The label may
sit on an enclosing `Describe`: the transformer reads the labels in scope for
the spec, own and inherited alike. Every budget is scaled by
`E2E_TIMEOUT_MULTIPLIER`. Do not hand-roll deadlines with
`time.After`; use `SpecTimeout` plus the framework's `Await`/`Eventually`
helpers so the multiplier applies.

## Labels

Speed/safety labels and feature labels live in `testlabels.go`; use the
constants, never string literals:

 - `fw.LabelSmoke`, `fw.LabelSlow`, `fw.LabelUpgrade`.
 - `fw.LabelDisruptive` — MUST be carried by any spec that performs a
   destructive or globally visible action: rebooting or fencing a node, killing
   agents, editing system node labels (for example
   `topology.kubernetes.io/zone`), removing a finalizer by hand, or writing to a
   raw block device. The label auto-injects `Serial` and the lowest spec
   priority, and the spec is skipped unless `E2E_ALLOW_DISRUPTIVE=true` or
   `E2E_RUN_ALL=true`. For the destructive helpers the requirement is
   **executable**, not just documented: they refuse to run in an unlabelled spec
   (see §Destructive operations are guarded at the call site).
 - `fw.LabelLongHaul` — for specs that are long because they *wait* for the
   cluster (an alert with `for: 15m` cannot be observed sooner). The label
   raises the default `SpecTimeout` to 30min and auto-injects the **highest**
   spec priority, so a parallel run hands the spec to a worker first and its
   wait overlaps with the rest of the suite. It deliberately does **not** inject
   `Serial`: serial specs run after every worker has exited, where the wait
   would overlap nothing. The spec is skipped unless `E2E_ALLOW_LONG_HAUL=true`
   or `E2E_RUN_ALL=true`; a focused run (`--focus`/`--focus-file`) bypasses this
   gate, and only this one — focusing says "run this spec", not "you may damage
   this cluster". A spec carrying both labels keeps the `Disruptive` placement,
   whether `Disruptive` is written on the spec itself or inherited from a parent
   container.
 - `fw.LabelFeature*` — one per functional area, for `--label-filter`.

`Disruptive` and `LongHaul` are the opt-in classes. Their gates share one
formula: the class variable **or** `E2E_RUN_ALL`, each parsed as a boolean
(`strconv.ParseBool`) — `true`/`1`/`t` enable the class, while unset, `false`
and any unrecognized value keep it skipped, and `E2E_RUN_ALL=true` wins over a
`false` class variable. The formula lives in one pure function
(`optInEnabled`, `e2e/pkg/framework/optin.go`) with a unit test per row, and no
`Skip` branch reads `os.Getenv` itself. Nothing in the repository sets these
variables — no workflow runs the e2e suites and `hack/run-e2e-new.sh` only picks
a label filter — so a document or a `Skip` message MUST tell the reader to
export them before the run, and MUST NOT suggest CI does it for them.

A spec that mutates state shared with other specs (node labels, RSP/RSC
selectors used by the whole suite) MUST also be `Serial` — which
`fw.LabelDisruptive` already provides.

## Arrange and cleanup

 - Arrange cluster state through framework builders (`f.TestRV()`,
   `f.TestRSC()`, `f.SetupLayout(...)`, …). Test objects auto-register their own
   cleanup; do not delete them by hand.
 - For anything the framework does not own (a system label, a temporary
   selector, a host-side process), `DeferCleanup` MUST be registered **before**
   the first mutation, and MUST restore the exact prior state — including the
   *absence* of a value. Snapshot the previous value (and whether it existed)
   before writing.
 - Never strip a finalizer during a healthy run. The only exception is a spec
   whose subject *is* the documented manual-escape recipe; such a spec carries
   `fw.LabelDisruptive` — `trvr.RemoveFinalizers` refuses to run without it — and
   the exception is written down in `e2e/full/RUNNING.md`.
 - Resource names come from the framework (`f.Name`, `f.UniqueName`, builder
   auto-naming) so leftovers are detectable on the next run. Random or
   timestamped names are forbidden.

## Assertions

 - Wait with `Await`/`Eventually` on a *fresh* snapshot; never assert survival
   against a snapshot taken before the disruption — observe the disruption
   taking effect first, then assert recovery.
 - Use `Always(...)` invariants (and `trv.ActivateSafetyInvariants()`) for
   properties that must hold for the whole window, not just at the end.
 - Prefer the atomic matchers in `e2e/pkg/framework/match` over sequences of
   independent `Await`s that can each pass on a different snapshot.
 - Ground truth beats derived status: when a claim is about the node
   (peer present/absent, quorum, device identity), assert it via
   `f.Drbdsetup(...)` in addition to the CR status.

## Skips

A skip is not a pass. Every `Skip` MUST state the missing precondition in its
message **and how to satisfy it**.

A `Skip` MUST be reachable for one of exactly three reasons:

 1. **The cluster is too small.** Prefer the declarative decorators in
    `e2e/pkg/framework/require` (for example `require.MinNodes(4, 1)`) over
    ad-hoc `Skip` calls.
 2. **The cluster lacks a capability the spec asserts against** — a CRD or a
    module that is not installed (for example `clusteralerts.deckhouse.io` on a
    stand without Deckhouse observability). Probe the capability, never the
    vendor's version string.
 3. **The spec belongs to an opt-in class whose gate is off** (`Disruptive`,
    `LongHaul` — see §Labels; the gate formula is the one stated there).

Anything else — an assertion that is inconvenient, a flake, an environment the
author did not feel like arranging — remains forbidden. If a scenario can be
built on any cluster (for example by carving an eligible set out with a
temporary label plus a dedicated RSC), build it — do not skip.

Every gate MUST be an explicit `Skip`. A silent `return`, an `if enabled { … }`
wrapped around the assertions, or an empty spec body all count as *Passed* in
the Ginkgo summary and manufacture a green run out of a check that never ran —
the failure mode opt-in specs are most exposed to, precisely because they are
off on most runs. Two questions must both answer "yes": with the gate on and the
feature broken, does the spec fail? With the gate off, does the spec appear in
the summary as *Skipped* with a reason?

## File organization

 - One scenario area per file: `<area>_test.go` (`r3_to_r2_migration_test.go`,
   `node_failure_quorum_test.go`, …).
 - Spec-local helpers shared by a few specs go into `<area>_helpers_test.go` in
   `package full`.
 - **The framework owns direct access.** A helper MUST live in
   `e2e/pkg/framework` when it reaches the cluster, a node, or a device by
   itself — a raw `f.Client` call (`Patch`, `Delete`, …), a hand-built exec, any
   path that is not already wrapped by a framework primitive. Such a helper
   follows the framework contract below: an unexported error-returning core plus
   unit tests.
 - **A `_test.go` file owns composition.** A thin composition of existing
   framework primitives with Gomega assertions, a pure projection of an object's
   state, and a Gomega matcher MAY stay in `<area>_helpers_test.go` — no matter
   how many areas reuse it. Breadth of reuse is not a reason to move a helper
   into the framework; direct access is.

## Test case documentation

Human-readable cases live in `e2e/full/TEST_CASES*.md`, split by area
(`TEST_CASES.md`, `TEST_CASES_LAYOUT.md`, `TEST_CASES_RSC_STATUS.md`,
`TEST_CASES_RVA_STATUS.md`). Each entry describes setup, action and the
observable assertions, and its title MUST match the Ginkgo spec text.

The *Ginkgo spec text* is the text of the `It(...)` verbatim: no numbering
prefix, no identifier suffix, no rewording — a title is greppable from the spec
and back. Everything else that identifies the entry MUST live on the metadata
line right below the title, never in the title itself: the `E2E-…` identifier,
the position of the case in the document, and the text of the enclosing
`Describe` container (one container usually spreads over several entries, so
dropping it makes an entry unfindable).

Adding, renaming or removing a spec MUST update the matching document in the
same change. Operational preconditions and runbook steps belong in
`e2e/full/RUNNING.md`.

# Framework helpers (`e2e/pkg/framework`)

## Contract

 - A helper is exported either as a method on `*Framework` (cluster- or
   node-scoped) or on a test object (`*TestRV`, `*TestRVA`, …).
 - Helpers report problems as Ginkgo failures (`Fail`, `Expect`), not as
   returned errors — a returned error would be silently ignorable in a spec.
 - **The failing logic MUST live in an unexported core that returns errors**;
   the exported helper is a thin wrapper that converts an error into a failure.
   This is what makes helpers unit-testable without a cluster.
 - Node access MUST go through the `nodeRunner` seam (`HostRun`,
   `HostRunNoRetry`, `DrbdsetupRun`), reached with `f.runner()`. Helpers MUST
   NOT build pod-exec requests themselves; unit tests substitute a stub runner
   with `&Framework{nodeRun: stub}` (see `node_runner_stub_test.go`).
 - A command that must not run twice MUST use `HostRunNoRetry`: `HostRun`
   re-executes on a transport error against a cached pod.
 - Document guarantees the Go types cannot express: idempotency, what is
   asserted, what is left behind, and which cleanup is auto-registered.
 - **A helper that damages state shared with the rest of the suite MUST call
   `fw.RequireDisruptiveSpec("<operation>")` as its first statement** — see
   §Destructive operations are guarded at the call site. A doc comment that only
   *states* the `fw.LabelDisruptive` requirement is not enough: the class gate
   cannot see the call, so the requirement is unenforced until the helper checks
   it.

## Unit tests

Every helper with non-trivial logic (parsing, classification, lifecycle,
retry/idempotency rules) MUST have unit tests in `e2e/pkg/framework` that run
without a cluster, driven by the stub runner. These tests are executed by
`go test ./...` in the module — compiling them is not enough. Cover at minimum:
parsers (including malformed/truncated input), each outcome of a classification
table, and — for any command that must not run twice — an assertion on the
number of recorded exec calls.

## Destructive operations are guarded at the call site

The class gate (`enforceDisruptive`) runs in `JustBeforeEach` and can only read
the labels the spec's author declared. It cannot know whether the spec is about
to call a destructive helper, so a spec that forgets `fw.LabelDisruptive` passes
the gate and then damages a shared stand anyway. The call site is the only place
where "this operation is destructive" and "these are the labels in scope" are
both known, so the check lives there:

```go
fw.RequireDisruptiveSpec("rebooting node " + nodeName)
```

`fw.RequireDisruptiveSpec` (`e2e/pkg/framework/disruptive.go`) stops the run
unless the executing spec carries `fw.LabelDisruptive`, on itself or on an
enclosing container, and its argument names the refused operation in the message.
It distinguishes three call sites, because a bare label check would misreport two
of them:

 - **No Ginkgo node running** — tree construction (a `Describe`/`Context` body, a
   package-level variable) or a plain go test. `CurrentSpecReport()` answers with
   a zero-value report whose label list is *empty*, so "the label is missing"
   would name the wrong cause. This is a programming error in the caller, so the
   guard **panics** with its own message instead of calling `Fail` — outside a
   node `Fail` has no spec to attribute the failure to and unwinds with Ginkgo's
   generic `UncaughtGinkgoPanic` text.
 - **A suite-level node** (`BeforeSuite`/`AfterSuite`/`SynchronizedBeforeSuite`/
   `SynchronizedAfterSuite`/`ReportBeforeSuite`/`ReportAfterSuite`/ a suite-level
   `DeferCleanup`). Such a node takes no decorators, so demanding a label would
   be unactionable: it fails asking for the call to be moved into a spec.
 - **A spec without the label** — it fails asking for `Label(fw.LabelDisruptive)`
   and names `E2E_ALLOW_DISRUPTIVE` / `E2E_RUN_ALL`, which the labelled spec then
   needs in order to run at all.

Guarded today: `f.RebootNode`, `f.StartIOWorkload`, `f.StartPodIOWorkload`,
`f.SetNodeLabel` and `trvr.RemoveFinalizers` — every framework helper that
damages state shared with the rest of the suite, so no spec can reach one of them
unlabelled, whether it calls it directly or through a wrapper. Adding a
destructive helper means adding the guard call in the same change.

A wrapper around a guarded helper does **not** repeat the guard: `startVolumeIO`
(`e2e/full/io_helpers_test.go`) only reads before it calls `f.StartIOWorkload`,
so the guard inside that helper already stops the same spec — and one requirement
checked in two places is two messages free to drift apart. A wrapper adds a guard
of its own only when it does destructive work *before* reaching the guarded
helper, and then names that work, not the helper's.

## Destructive node operations

Two framework helpers act directly on a node's host; both are documented here
because specs must use them exactly as designed.

### `f.RebootNode(ctx, nodeName)`

Reboots the host and returns a `*fw.NodeReboot` handle. The handle exists so a
spec can observe the outage itself: `RebootNode` returns as soon as the reboot
is proven to have started, and `reboot.AwaitCompleted(ctx)` blocks until the
node is back. A spec that only needs the node back calls both in sequence.

 - The calling spec MUST carry `fw.LabelDisruptive`; `RebootNode` enforces it
   before anything is executed on the host (§Destructive operations are guarded
   at the call site).
 - The reboot command is executed through a **no-retry** exec: `execOnNode`
   retries a transport error against a freshly resolved pod, which for
   `systemctl reboot` risks a second reboot.
 - The command prints the `REBOOT_STARTED` marker and runs `sync` before
   `systemctl reboot`, in the same shell invocation.
 - Outcome rules: a non-zero exit always fails; a transport error is accepted
   only when the marker was already received (the connection dies with the
   node); no marker means the reboot is not proven to have started and the
   spec fails.
 - Completion criterion (`AwaitCompleted`): the node's `status.nodeInfo.bootID`
   changed **and** the node is currently `Ready=True`. Observing `Ready=False`
   is a progress signal only — a fast reboot may never be published as
   NotReady, so waiting for it would be flaky.

### `f.StartIOWorkload(ctx, opts)`

Starts a persistent raw-device writer on a node and provides an
`*fw.IOWorkload` handle with `Observe`, `AwaitProgress`, `Stop` and `Cleanup`.
Use it whenever a spec claims that "I/O keeps flowing"; asserting conditions
alone proves nothing about the data path.

 - The calling spec MUST carry `fw.LabelDisruptive`; `StartIOWorkload` enforces
   it before the options are even validated (§Destructive operations are guarded
   at the call site).
 - Specs in `e2e/full` reach it through `startVolumeIO`
   (`io_helpers_test.go`), which resolves the device and the expected identity
   from the RVA and the node's DRBD resource. Use that wrapper rather than
   calling `f.StartIOWorkload` directly.
 - The device is `RVA.Status.DevicePath` and nothing else; the workload runs in
   the node's host namespaces through the same sds-node-configurator + nsenter
   channel as the other node helpers.
 - The writer is a **persistent host process** that survives the exec session.
   It writes `{sequence, checksum}` records into a bounded ring of aligned
   slots and appends a heartbeat journal on the node. A heartbeat is published
   only after the record was written, `fdatasync`-ed, read back and verified —
   so a heartbeat proves a device write, not a userspace write.
 - `Observe` is a short exec that reads the journal tail; stall detection uses
   the node's own clock against `MaxHeartbeatGap`. Early exit and I/O errors
   are reported through the journal's termination record.
 - Device safety: the device path is validated before the device is opened
   (non-empty, resolving to a canonical `/dev/drbd<N>`), the expected minor is
   taken from `drbdsetup` on the node (kernel ground truth, independent of the
   API), the device is opened exactly once and identity is verified with
   `fstat` on that open descriptor, and all I/O uses that same descriptor.
   Any mismatch fails before a single byte is written.
 - `StartIOWorkload` registers its `DeferCleanup` **before** spawning, so a
   failing spec can never leave a process writing to the device. `Cleanup` is
   idempotent, stops the writer, verifies the last journal record, and only
   then removes its files — so it always runs before the RVA teardown
   registered earlier (Ginkgo cleanups are LIFO). Handles sharing a `runID`
   share one writer: the first cleanup ends it and takes the journal with it,
   and the others find nothing left to verify.
 - `start` is idempotent per `runID`: the writer publishes a
   `{runID, PID, processStartTime, BootID, device}` marker atomically before
   opening the device, and `observe`/`stop`/`cleanup` locate the process by
   `runID` and signal it only on a full marker match (a mismatching BootID or
   start time means the PID was reused — the process is left alone). A second
   `start` adopts the running writer instead of spawning another one, but only
   when the marker names the same device: adoption makes another process's I/O
   this handle's evidence, so a `runID` reused for a different device is an
   error, not a silent adoption.
 - `opts.RunID` defaults to a name unique **per call**, so two workloads in one
   spec never collide (`f.UniqueName("io")` would not do: a fixed suffix is
   stable within a spec, not unique per call). Pass an explicit `runID` only to
   address a writer on purpose — and then pass the same `DevicePath` and leave
   the cleanup to a single handle.
 - A marker whose process is provably gone (the node rebooted, or the PID was
   reused) is cleared together with its journal on the next `start`, so the
   same `runID` can be restarted after a reboot. Consequence for specs: the
   journal survives a reboot for inspection, but restarting the same `runID`
   discards it — observe across the outage rather than restarting, unless the
   pre-reboot history is not needed.
