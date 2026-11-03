#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
remote_put .github/workflows/gcd-compact.yml '# written by gcd
on:
  schedule:
    - cron: "17 3 * * 0"
jobs:
  compact:
    steps:
      - env:
      GCD_VERSION: "0.1.0-rc1"
      GCD_URL: "https://github.com/Hanson-Tsai/gcd/releases/download/v0.1.0-rc1/gcd_0.1.0-rc1_linux_amd64.tar.gz"
      GCD_SHA256: "0000000000000000000000000000000000000000000000000000000000000000"'
put "$HERE" new.md "n"
