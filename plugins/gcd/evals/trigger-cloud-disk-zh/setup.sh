#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
put "$HERE" mine.md "written here"
put "$OTHER" theirs.md "written on the other machine"
other sync --json >/dev/null
put "$HERE" 'a:b.txt' "a name Windows cannot hold"
