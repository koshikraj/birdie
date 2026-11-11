#!/bin/sh
# Prints the license text of everything compiled into the birdie binaries, which
# `make dist` publishes as THIRD_PARTY_NOTICES.
#   scripts/third-party-notices.sh linux/amd64 darwin/arm64 ...
# Each platform compiles in different modules, so pass every one being released.
set -eu

# Keeps the output identical from one machine to the next.
export LC_ALL=C

die() {
	echo "third-party-notices: $*" >&2
	exit 1
}

# license_files ROOT PACKAGE_DIR prints the license files that apply to a package:
# those in its own directory and in each parent, up to the root of its module.
license_files() {
	rel=${2#"$1"}
	while :; do
		find "$1$rel" -maxdepth 1 -type f ! -name '*.go' \( \
			-iname 'licen[cs]e*' -o -iname 'copying*' -o -iname 'copyright*' -o \
			-iname 'notice*' -o -iname 'patents*' -o -iname 'unlicense*' \)
		[ -n "$rel" ] || return 0
		rel=${rel%/*}
	done
}

[ $# -gt 0 ] || die "usage: $0 OS/ARCH..."
cd "$(dirname "$0")/.."

tab="$(printf '\t')"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# One line per compiled package: module and version, module root, package directory.
# Every binary also contains Go's own standard library and runtime.
goroot="$(go env GOROOT)"
printf 'Go standard library %s\t%s\t%s\n' "$(go env GOVERSION)" "$goroot" "$goroot" >"$tmp/packages"
for platform in "$@"; do
	CGO_ENABLED=0 GOOS="${platform%/*}" GOARCH="${platform#*/}" go list -deps \
		-f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{"\t"}}{{.Dir}}{{"\t"}}{{$.Dir}}{{end}}{{end}}' \
		./cmd/birdie >>"$tmp/packages"
done
cut -f1,2 "$tmp/packages" | sort -u >"$tmp/modules"

{
	echo "THIRD-PARTY NOTICES"
	echo
	echo "The birdie release binaries include the software listed here, though not"
	echo "every binary has all of it, since that depends on the platform. The notices"
	echo "of each one follow in full, so some mention code birdie doesn't compile in."
	echo
	cut -f1 "$tmp/modules" | sed 's/^/  /'
} >"$tmp/notices"

while IFS="$tab" read -r module root; do
	awk -F '\t' -v m="$module" '$1 == m { print $3 }' "$tmp/packages" |
		while IFS= read -r package; do license_files "$root" "$package"; done |
		sort -u >"$tmp/licenses"
	# Nothing ships without its notices, so a new dependency without any has to
	# be looked at by a person before the next release can go out.
	[ -s "$tmp/licenses" ] || die "no license file found for $module in $root"

	printf '\n================================================================================\n'
	printf '%s\n' "$module"
	while IFS= read -r file; do
		printf '\n--- %s\n\n' "${file#"$root"/}"
		cat "$file"
	done <"$tmp/licenses"
done <"$tmp/modules" >>"$tmp/notices"

cat "$tmp/notices"
