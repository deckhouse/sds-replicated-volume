## Patches

### 001-fix-linstor-spec.patch

Fix linstor.spec

Fixes error 'error: Group field must be present in package: linstor-common' when building RPM package (for ALT Linux)

### 002-fix-symlinks.patch

Automatically fix symlinks for devices

This change is workaround for specific set of issues, often related to udev,
which lead to the disappearance of symlinks for LVM devices on a working system.
These issues commonly manifest during device resizing and deactivation,
causing LINSTOR exceptions when accessing DRBD super-block of volume.

- Upstream: https://github.com/LINBIT/linstor-server/pull/370

### 003-fix-getdbversion-avoid-list-all-crds.patch

Do not list all CustomResourceDefinitions in `getDbVersion()`.

fabric8 typed deserialization of the full CRD list fails when foreign CRDs
contain unknown OpenAPI extensions (e.g. Deckhouse
`x-kubernetes-sensitive-data` on `modulesources` / `packagerepositories`),
which crashes `linstor-controller` during `DbK8sCrdInitializer`
(`UnrecognizedPropertyException` → Database initialization error).

Probe `LinstorVersion` resources instead; treat HTTP 404 as missing CRD.

### 004-fix-fabric8-ignore-unknown-properties.patch

Configure the controller fabric8 `KubernetesClient` with
`FAIL_ON_UNKNOWN_PROPERTIES=false`.

General defense: any typed deserialize of cluster objects (including foreign
CRD OpenAPI schemas with vendor extensions) must not abort LINSTOR startup.
Does not replace 003 — 003 removes the unnecessary all-CRD list; 004 hardens
the client against unknown fields elsewhere.
