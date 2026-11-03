---
description: Restore an earlier or deleted version of a file from gcd history
argument-hint: "<path> [--at <when>] [--disk <name>]"
disable-model-invocation: true
---

Use the `gcd` skill to restore a file, following its restore steps
(`references/history.md`): find the version with `gcd log --json` when the user
describes it in words, run `gcd restore --json`, ask before `--force`, and
remind the user that the next sync uploads the restored file.

Arguments from the user (the path, and optionally `--at <when>` and
`--disk <name>`): $ARGUMENTS
