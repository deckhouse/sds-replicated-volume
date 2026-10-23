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
