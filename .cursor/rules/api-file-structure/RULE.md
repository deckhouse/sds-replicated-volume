---
description: API file structure and conventions (sds-replicated-volume)
globs:
  - "api/**/*.go"
  - "!api/linstor/**/*.go"
alwaysApply: true
---

- Object prefixes (MUST):
  - Use short prefixes: `rv`, `rvr`, `rva`, `rsc`, `rsp`.

- File naming per object (MUST):
  - `<objprefix>_types.go`: API types (kubebuilder tags), object/spec/status structs, adapters for interfaces (e.g. GetConditions/SetConditions) and tightly coupled constants/types and pure set/get/has helpers (no I/O, no external context).
  - `<objprefix>_conditions.go`: condition Type/Reason constants for the object.
    - MAY be absent if the API object exposes `.status.conditions` but there are no standardized/used conditions yet (do not create empty placeholder constants).
  - `<objprefix>_custom_logic_that_should_not_be_here.go`: non-trivial/domain logic helpers (everything that does not fit `*_types.go`).

- Common file naming (MUST):
  - `common_types.go`: shared types/enums/constants for the API package.
  - `common_helpers.go`: shared pure helpers used across API types.
  - `labels.go`: well-known label keys (constants).
  - `finalizers.go`: module finalizer constants.
  - `register.go`: scheme registration.
