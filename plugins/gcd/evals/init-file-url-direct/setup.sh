#!/usr/bin/env bash
. "$(dirname "$0")/../lib/world.sh"
world_init
git init -q --bare --initial-branch=main test-remote.git
