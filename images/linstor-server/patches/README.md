## Patches

### Automatically fix symlinks for devices

This change is workaround for specific set of issues, often related to udev,
which lead to the disappearance of symlinks for LVM devices on a working system.
These issues commonly manifest during device resizing and deactivation,
causing LINSTOR exceptions when accessing DRBD super-block of volume.

- Upstream: https://github.com/LINBIT/linstor-server/pull/370

### Fix linstor.spec

Fixes error 'error: Group field must be present in package: linstor-common' when building RPM package (for ALT Linux)

### linstor-python-api.spec

Spec file for RPM building of Linstor's python-api library. Spec-file taked from 'make rpm' and then 'cat build/bdist.linux-x86_64/rpm/SPECS/python-linstor.spec' command in build-container.

### linstor-client.spec

Spec file for RPM building of Linstor's client. Spec-file taked from 'make rpm' and then 'cat build/bdist.linux-x86_64/rpm/SPECS/linstor-client.spec' command in build-container.
