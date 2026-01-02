---
description: Go test rules
globs:
  - "**/*_test.go"
alwaysApply: true
---

- Test fixtures & I/O (MUST):
  - Prefer embedding static fixtures with `//go:embed` into a `[]byte`.
  - Do NOT read fixtures from disk at runtime unless embedding is impossible.

- Test payload minimalism (MUST):
  - Only include fields that are asserted in the test.
  - Prefer small, explicit test bodies over helpers until a helper is reused in 3+ places.

- Struct tags in tests (MUST):
  - Include only the codec actually used by the test.
  - Do NOT duplicate `json` and `yaml` tags unless both are parsed in the same code path.
  - Prefer relying on field names; add a `yaml` tag only when the YAML key differs and renaming the field would hurt clarity.

- Topology tests specifics (MUST):
  - Parse YAML fixtures into existing structs without adding extra tags.
  - Embed testdata (e.g., `testdata/tests.yaml`) and unmarshal directly; avoid runtime I/O.
