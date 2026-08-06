#!/bin/sh
# Install repo git hooks into .git/hooks (no git config changes).
set -e
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
hooks="$root/.githooks"
target="$root/.git/hooks"

if [ ! -d "$target" ]; then
	echo "error: $target not found — run from a git checkout" >&2
	exit 1
fi

for name in commit-msg prepare-commit-msg; do
	src="$hooks/$name"
	dst="$target/$name"
	if [ ! -f "$src" ]; then
		echo "error: missing $src" >&2
		exit 1
	fi
	ln -sfn "../../.githooks/$name" "$dst"
	echo "installed $dst -> ../../.githooks/$name"
done

# Shared helper must be executable; hooks invoke it by relative path.
chmod +x "$hooks"/strip-llm-coauthors.sh "$hooks"/commit-msg "$hooks"/prepare-commit-msg
echo "done"
