#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
# The other machine uploads the file 10 s after the agent starts gcd watch
# (or after 5 minutes without one), so the upload cannot race the watch start.
nohup sh -c "i=0; while [ \$i -lt 300 ] && ! pgrep -f 'gcd[ ]watch' >/dev/null; do sleep 1; i=\$((i+1)); done; sleep 10; mkdir -p '$OTHER/results'; printf 'TOTAL: 42\nsecond line\n' >'$OTHER/results/report.md'; GCD_HOME='$WORLD/.other/home' gcd sync >/dev/null 2>&1" >/dev/null 2>&1 &
disown || true
