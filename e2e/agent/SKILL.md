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

There are three categories of test helpers, ordered from heaviest to lightest:

 - **Setup helpers** (`SetupX`) — have at least one Arrange step (and,
   therefore, Cleanup). They create or modify state in the environment and
   provide a service with guarantees about that state.
 - **Discovery helpers** (`DiscoverX`) — discovery-only. They discover and
   validate existing state but do not arrange anything.
 - **Initialization helpers** (`InitializeX`) — the lightest category. Unlike
   discovery helpers, they do not look at environment state at all. They only
   construct and return a value or service. They MUST have Provide and MAY have
   Require; they have no other steps.

All test helpers MUST follow common naming and signature conventions (see
Signature below). Each helper is composed of the following steps (see
corresponding sections below):

 - Require — all categories
 - Discover — setup and discovery helpers only
 - Arrange/Act — setup helpers only
 - Cleanup — setup helpers only
 - Assert — setup and discovery helpers only
 - Provide — all categories

### Signature

Here's a sample signature for a state `X`, which is provided by a SetupX setup
helper.

```go
// SetupX Provides X, which is guaranteed to be ..., can be accessed via ...
func SetupX(t *testing.T, <REQUIREMENTS>) <PROVIDED_X_SERVICE> {
    // May call any test helpers (Setup, Discover, Initialize).
}
```

Discovery and initialization helpers follow the same pattern with their
respective prefixes:

```go
// DiscoverX Discovers existing X, which is guaranteed to be ...
func DiscoverX(t *testing.T, <REQUIREMENTS>) <PROVIDED_X_SERVICE> {
    // May call other Discover and Initialize helpers, but NOT Setup helpers.
}

// InitializeX Initializes X service for ...
func InitializeX(t *testing.T, <REQUIREMENTS>) <PROVIDED_X_SERVICE> {
    // Must not call any other test helpers.
}
```

`t` is the main argument, which always comes first.

### Require

This is what the test helper requires from the environment to be true. In order
to enforce these requirements, it MAY accept arguments after `t` (compile-time
requirements) and also do its own validation of them at the beginning of the
test helper.

A failed requirement is a reason to fail the test (fatal).

Requirements that are not strictly guaranteed by the Go type system MUST be
validated and MUST be described in the test helper's documentation.

Configuration of test helpers should also be treated as an environment
requirement, since from the test helper's perspective it is not determined where
an argument came from — caller's code or environment.

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
that, setup helpers MUST use `t.Cleanup(func() { /* do cleanup here */ })`.

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

For initialization helpers, Provide is the only required step (alongside
optional Require). The helper just constructs and returns a value or service.

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
idempotent.

**Discovery helpers** (`DiscoverX`) perform only this step: they discover and
validate existing state, then provide it. They never have Arrange or Cleanup
steps. If the state is not found or not valid, it is a fatal test failure.

## Root test

The root test is a normal Go test that follows standard Go testing conventions.
The key difference from usual unit tests is that it is an integration test
running against a real environment.

### Hierarchical structure

Arranging state in an environment is costly — we cannot afford doing it for each
test case individually. Instead, tests are structured to reuse the environment
as much as possible. This means a hierarchical test structure using `t.Run` for
subtests, where subtests run sequentially and reuse state defined in the parent
scope.

### Relation to test helpers

In terms of test helpers, the root test behaves like a setup helper without
Require and Provide: it arranges state, registers cleanup, and runs subtests.
Subtests have a Require part (they depend on state from the parent scope) but do
not provide anything.

One important difference from setup helpers: in the root test, all arranged
state is cleaned up before the test exits (via `t.Cleanup`), so no state leaks
out.

### Configuration

The root test discovers its configuration from a JSON file on the filesystem,
with environment-specific variables. Keep a `.env.example.json` in the test
directory to document the expected configuration shape and help set up new
environments.

## Test cases

The full hierarchy of test cases is maintained in `e2e/agent/TESTCASES.md`. This
file MUST correspond to the actual Go tests: hierarchy, test names, and
descriptions of what each test does.

Whenever a test case is added, renamed, or removed in `TESTCASES.md`, the
corresponding Go test MUST be updated to match, and vice versa — changes to Go
tests MUST be reflected in `TESTCASES.md`.
