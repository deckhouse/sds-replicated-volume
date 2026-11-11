## Patches

### 001-linstor-scheduler-extender.patch

Scheduler extender

Extend scheduler with HTTPS support and our own logic.

### 002-fix-cve.patch

CVE fix

### 003-fix-cve.patch

CVE fix

### 004-fix-cve.patch

CVE fix

Bump golang.org/x/net, golang.org/x/crypto, golang.org/x/sys, golang.org/x/text, google.golang.org/grpc, google.golang.org/protobuf, github.com/golang/glog and github.com/moby/spdystream to versions without known vulnerabilities.

### 005-fix-cve-grpc-x-crypto.patch

Raises `google.golang.org/grpc` v1.82.1 -> **v1.83.2** for **CVE-2026-84304** and
**CVE-2026-84303** (fixed in 1.83.1) and **CVE-2026-84445** (fixed in 1.83.2), and
`golang.org/x/crypto` v0.53.0 -> **v0.57.0** for **CVE-2026-56854**,
**CVE-2026-56855** and **CVE-2026-78662**. These were the Trivy findings against
the built `linstor-scheduler-extender` binary. The `go` directive moves 1.23.0 ->
1.26.0, the language version x/crypto v0.57.0 requires.

The patch also pins `google.golang.org/api` at v0.110.0 with a `replace`, the
version the module graph resolved before this bump. Without the pin grpc v1.83.2
carries `google.golang.org/api` to v0.264.0, which brings
`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` v0.20.0 -> v0.61.0
and puts `github.com/go-logr/stdr` on the build path -- and stdr imports
`github.com/go-logr/logr/funcr`, which does not exist in the `logr` v0.3.0 that
upstream's own `replace` pins for stork. The build then fails while resolving
imports. `google.golang.org/api` reaches this binary only through
stork -> gcp-compute-persistent-disk-csi-driver, a code path this scheduler
extender does not use.

Applied after 004; `go.mod` / `go.sum` only.

### 006-fix-cve-x-net.patch

Raises `golang.org/x/net` v0.58.0 -> **v0.60.0** for **CVE-2026-78669**, the
Trivy finding against the built `linstor-scheduler-extender` binary
(`golang.org/x/text` is already at the fixed v0.42.0). Applied after 005;
`go.mod` / `go.sum` only.

### stork/stork.patch

Changes to stork v2.11.5 (`LINSTOR_SCHEDULER_STORK` in `oss.yaml`), the library
that serves the extender's `filter` and `prioritize` endpoints. The build copies
`stork/new-files/` into the stork source and then applies `stork/*.patch` in glob
order. All stork changes are kept in this one patch: a second patch for
`pkg/extender/extender.go` applies only if its name sorts after `stork.patch`.

- HTTPS, log level and the shared informer cache (`stork/new-files/pkg/cache`)
  for PVC, node and pod lookups.
- Trace logging of each request; `nodenames` requests (`nodeCacheCapable`) are
  filled with the Node objects.
- Filter responses preserve the request format: `NodeNames` requests receive
  filtered names so the scheduler can reuse its cached node information;
  `Nodes` requests receive filtered Node objects for compatibility.
- Dry run: the Deckhouse kube-scheduler sets `"dryRun": true` in `filter` and
  `prioritize` requests while it simulates scheduling (`POST /simulate`, for
  example for the descheduler). The Pod in such a request is a copy of a real Pod
  and is never bound. For these requests the extender records no
  `FailedScheduling` Event on the Pod; the response (nodes, scores, error text and
  status) is the same as without `dryRun`. The log lines of these failures carry
  the field `dryRun=true`, so a simulation can be told apart from a real
  scheduling failure in the extender log. `dryRun` must be a JSON boolean: any
  other type fails to decode, and the request is answered with 400
  "Decode error". Requests without the field work as before.
  `pkg/extender/dryrun_test.go` (build tag `unittest`) checks this for `filter`
  and `prioritize`.
