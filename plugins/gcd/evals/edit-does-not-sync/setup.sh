#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
put "$HERE" todo.md "milk"
here sync --json >/dev/null
