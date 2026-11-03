#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
for i in 0 1 2 3 4 5 6 7 8 9; do put "$HERE" "doc$i.md" "$i"; done
here sync --json >/dev/null
rm -f "$HERE"/doc*.md
