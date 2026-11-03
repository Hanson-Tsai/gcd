---
description: Show what the next gcd sync would upload, download and leave alone
argument-hint: "[--disk <name>]"
disable-model-invocation: true
---

Use the `gcd` skill to show the status of the user's Disk: run the version
check, then `gcd status --json` and summarize it, as the skill describes.

Arguments from the user (pass `--disk <name>` through; empty means the Disk of
the current directory): $ARGUMENTS
