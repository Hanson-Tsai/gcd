#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
put "$HERE" report.md "draft"
put "$OTHER" todo.md "milk"
other sync --json >/dev/null
