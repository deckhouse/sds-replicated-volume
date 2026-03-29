---
name: writing-e2e-tests
description: Write and maintain e2e integration tests and keep e2e/agent/TESTCASES.md in sync with Go test code. Use when editing test code under e2e/ or e2e/agent/TESTCASES.md.
---

# Writing e2e tests

This skill covers writing and maintaining end-to-end tests for the e2e agent.

 - e2e test code lives in `e2e/`.
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

The first argument of every test helper MUST be `e *envtesting.E`.

```go
// SetupX Provides X, which is guaranteed to be ..., can be accessed via ...
func SetupX(e *envtesting.E, <REQUIREMENTS>) <PROVIDED_X_SERVICE> {
    // May call any test helpers (Setup, Discover).
}

// DiscoverX Discovers existing X, which is guaranteed to be ...
func DiscoverX(e *envtesting.E, <REQUIREMENTS>) <PROVIDED_X_SERVICE> {
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
func TestMain(t *testing.T) {
    e := envtesting.Discover(t)
    client := DiscoverClient(e)

    // Arrange: set up shared environment state.
    storageClass := SetupStorageClass(e, client)
    // e.Cleanup registered inside SetupStorageClass.

    e.Run("WithSingleVolume", func(e *envtesting.E) {
        // Arrange: narrow state for this group of subtests.
        volume := SetupVolume(e, client, storageClass)

        e.Run("VolumeIsAccessible", func(e *envtesting.E) {
            // Test case: uses volume from parent scope, no extra arrange.
        })

        e.Run("VolumeCanBeResized", func(e *envtesting.E) {
            // Test case: uses volume from parent scope, acts and asserts.
        })
    })

    e.Run("WithReplicatedVolume", func(e *envtesting.E) {
        // Arrange: different state, same storageClass from root scope.
        volume := SetupReplicatedVolume(e, client, storageClass)

        e.Run("ReplicationIsHealthy", func(e *envtesting.E) {
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
