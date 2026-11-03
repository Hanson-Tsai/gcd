# Offline gcd world for the eval cases. A case's setup.sh sources this file
# from the run's empty workspace (the agent's working directory) and calls
# world_init, which builds:
#
#   remote.git/      a bare git repo: the "GitHub repo", reached as file://
#   notes/           the Disk "notes" of this machine, the agent's machine.
#                    Its gcd home is gcd's default under $HOME, the run's
#                    temporary home, so the agent's plain `gcd` finds it.
#   .other/notes/    the same Disk on a second machine, driven with `other`,
#                    whose gcd home is .other/home (GCD_HOME).
#
# No GitHub and no token are involved. The repo config sets short watch
# timings (debounce 1 s, poll 2 s) so watch cases finish quickly.
#
# Helpers: here (gcd on this machine), other (gcd on the second machine),
# put <machine-folder> <path> <content>, remote_put <path> <content>.

set -eu

if ! command -v gcd >/dev/null 2>&1; then
	echo "world.sh: gcd is not on PATH" >&2
	exit 1
fi
command -v git >/dev/null 2>&1 || { echo "world.sh: git is not on PATH" >&2; exit 1; }

WORLD=$(pwd -P)
REMOTE="file://$WORLD/remote.git"
HERE="$WORLD/notes"
OTHER="$WORLD/.other/notes"
unset GCD_HOME GCD_TOKEN XDG_DATA_HOME

here() { env -u GCD_HOME gcd "$@"; }
other() { GCD_HOME="$WORLD/.other/home" gcd "$@"; }

# put <folder> <path> <content>: write a file into a machine's folder.
put() {
	mkdir -p "$(dirname "$1/$2")"
	printf '%s\n' "$3" >"$1/$2"
}

# remote_put <path> <content>: commit one file to the remote directly, as a
# different git client would (used for managed files such as the workflow).
remote_put() {
	tmp="$WORLD/.other/clone"
	rm -rf "$tmp"
	git clone -q "$REMOTE" "$tmp" 2>/dev/null
	mkdir -p "$(dirname "$tmp/$1")"
	printf '%s\n' "$2" >"$tmp/$1"
	git -C "$tmp" add "$1"
	git -C "$tmp" -c user.name=eval -c user.email=eval@example.invalid commit -qm "eval: $1"
	git -C "$tmp" push -q origin HEAD:main
	rm -rf "$tmp"
}

world_init() {
	git init -q --bare --initial-branch=main "$WORLD/remote.git"
	mkdir -p "$WORLD/.other"
	remote_put .gcd/config.toml "format = 1
debounce_seconds = 1
poll_seconds = 2"
	here init --disk notes "$REMOTE" "$HERE" >/dev/null
	other init --disk notes "$REMOTE" "$OTHER" >/dev/null
}
