#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
mkdir -p bin
cat >bin/gcd <<'EOF'
#!/bin/sh
# An old gcd: it reports 0.1.0-rc1 and has no --json anywhere else.
case "$*" in
  "version --json") echo '{"version":"0.1.0-rc1","os":"linux","arch":"amd64","dev":false}' ;;
  *) echo "flag provided but not defined: -json" >&2; exit 2 ;;
esac
EOF
chmod +x bin/gcd
