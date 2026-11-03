#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
put "$HERE" plan.md "version one"
put "$HERE" keep.md "keep"
here sync --json >/dev/null
put "$HERE" plan.md "unsynced local edits"
