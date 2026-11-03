#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
put "$HERE" plan.md "base"
here sync --json >/dev/null
other sync --json >/dev/null
put "$OTHER" plan.md "plan from the other machine"
other sync --json >/dev/null
put "$HERE" plan.md "plan from this machine"
