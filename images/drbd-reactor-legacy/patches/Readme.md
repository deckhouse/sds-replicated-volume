# Patches

These patches are packaging-only fixes for building upstream `drbd-reactor` in the legacy LINSTOR image. They must not carry DRBD kernel/userspace behavior changes.

## fix-drbd-reactor-spec.patch

Remove systemd build-requirement because ALTLinux is based on SysV, not Systemd.

## fix-rust-dangerous-implicit-autorefs.patch

Disable the `dangerous_implicit_autorefs` lint when building with newer ALT Rust.
