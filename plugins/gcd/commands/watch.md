---
description: Wait until gcd downloads a file from another machine (gcd watch --until)
argument-hint: "[path] [--disk <name>]"
disable-model-invocation: true
---

Use the `gcd` skill and follow its watch steps (`references/watch.md`).

- With a path: wait for that file with
  `gcd watch --json --disk <name> --until <path>` in the background, report
  conflicts and `needs_confirm` events at once, and tell the user when the file
  has arrived.
- Without a path: the user explicitly asks for open-ended watching. Run
  `gcd watch --json --disk <name>` in the background, tell the user it keeps
  syncing until stopped, and stop it when the user asks.

Arguments from the user: $ARGUMENTS
