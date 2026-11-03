---
description: Sync a gcd Disk, checking status first and asking before deletes or conflicts
argument-hint: "[--disk <name>]"
disable-model-invocation: true
---

The user asks for a gcd sync. Use the `gcd` skill and follow its sync steps
(`references/sync.md`) exactly: status first, sync directly only when there are
just additions and modifications, and stop to ask before deletes, conflicts or
`needs_confirm`. This command is the user's request to sync; it is not
approval for any deletion, conflict or `--yes`.

Arguments from the user (pass `--disk <name>` through; empty means the Disk of
the current directory): $ARGUMENTS
