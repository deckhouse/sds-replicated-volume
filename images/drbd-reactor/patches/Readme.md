# Patches

## fix-drbd-reactor-spec.patch

Remove systemd build-requirement because ALTLinux is based on SysV, not Systemd.

## fix-rust-dangerous-implicit-autorefs.patch

Отключить lint `dangerous_implicit_autorefs` при сборке на Rust из нового ALT (clap 2 + `crate_authors!`).
