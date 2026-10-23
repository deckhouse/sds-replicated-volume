## Patches

### 001-cve-fix.patch

CVE fix

### 002-fix-cve.patch

CVE fix

Bump golang.org/x/net, golang.org/x/sys, golang.org/x/text, google.golang.org/grpc and google.golang.org/protobuf to versions without known vulnerabilities.

### 003-fix-cve-grpc.patch

Raises `google.golang.org/grpc` v1.82.1 -> **v1.83.2** for **CVE-2026-84304** and
**CVE-2026-84303** (fixed in 1.83.1) and **CVE-2026-84445** (fixed in 1.83.2), the
Trivy findings against the built `linstor-affinity-controller` binary. Applied
after 002; `go.mod` / `go.sum` only.

### 004-fix-cve.patch

Raises `golang.org/x/net` v0.58.0 -> **v0.60.0** for **CVE-2026-78669** and
`golang.org/x/text` v0.41.0 -> **v0.42.0** for **CVE-2026-56851**. Applied after
003; `go.mod` / `go.sum` only.
