#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
# The other machine uploads the file about 40 s after setup ends.
nohup sh -c "sleep 40; mkdir -p '$OTHER/results'; printf 'TOTAL: 42\nsecond line\n' >'$OTHER/results/report.md'; GCD_HOME='$WORLD/.other/home' gcd sync >/dev/null 2>&1" >/dev/null 2>&1 &
disown || true
