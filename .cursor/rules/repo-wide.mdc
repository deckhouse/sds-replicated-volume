---
description: Repository-wide Cursor Context Rules for sds-replicated-volume-2
globs:
  - "**/*"
alwaysApply: true
---

- Formatting & style (MUST):
  - Match existing formatting and indentation exactly.

- Change hygiene / cleanup (MUST):
  - If I create a file and later replace it with a correct alternative, I MUST remove the now-invalid file(s) in the same change.

- Dialogue adherence (MUST):
  - User answers are authoritative context.
  - If I ask a question and receive an answer, subsequent actions MUST align with that answer and MUST NOT contradict or ignore it.

- File moves/renames (MUST):
  - When moving or renaming files, preserve Git history by using `git mv` (or an equivalent Git-aware rename).
  - Do NOT implement a move as "create new file + delete old file".

- Git commit messages (MUST):
  - Use English for commit messages.
  - Include a `Signed-off-by: Name <email>` line in every commit (prefer `git commit -s`).
  - Prefer prefixing the subject with a component in square brackets, e.g. `[controller] Fix ...`, `[api] Add ...`.
  - If the change is non-trivial, add a short body listing the key changes; for small changes, the subject alone is enough.
  - When generating a commit message, consider the full diff (don’t skimp on context), including:
    - Staged/cached changes (index)
    - Contents of deleted files
