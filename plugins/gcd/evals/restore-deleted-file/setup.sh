#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
put "$HERE" ideas.md "a good idea"
put "$HERE" keep.md "keep"
here sync --json >/dev/null
rm "$HERE/ideas.md"
here sync --json >/dev/null
