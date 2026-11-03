---
description: Set up gcd on this machine and get the exact gcd init line to run
argument-hint: "[<owner/repo> <folder>]"
disable-model-invocation: true
---

Use the `gcd` skill and follow its init steps (`references/setup.md`): check
that gcd is installed and new enough, explain the three token permissions, and
give the user the exact `gcd init <owner/repo> <folder>` line to run in their
own terminal. The user types the token there, never in this conversation. A
`file://` repo you may init yourself.

Arguments from the user (the repo and folder, if given; ask for any that are
missing): $ARGUMENTS
