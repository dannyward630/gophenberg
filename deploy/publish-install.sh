#!/bin/sh
set -eu

# say prints its arguments as one line on standard output.
say() {
	printf '%s\n' "$*"
}

# stop prints why publish-install.sh stops on standard error and ends it with exit code 1.
stop() {
	printf 'publish-install.sh: %s\n' "$*" >&2
	exit 1
}

# release_version stops unless the value is a stable release version of three numbers, naming what holds it.
release_version() {
	case "$1" in
	*[!0-9.]* | *..* | .* | *. | *.*.*.*) ;;
	*.*.*) return 0 ;;
	esac
	stop "$2 names $1, which is not a stable release version of three numbers"
}

# read_checksum sets sum to the hex of the one sha256 checksum the record lists for the Linux processor, or stops.
read_checksum() {
	checksum=$(jq -r --arg id "linux-$1" \
		'[.artifacts.package[] | select(.id == $id) | .checksum] | if length == 1 then .[0] else "" end' "$record")
	sum=${checksum#sha256:}
	case "$sum" in
	"$checksum" | *[!0-9a-f]*) sum= ;;
	esac
	[ "${#sum}" = 64 ] || stop "the release record lists no single sha256 checksum of the linux/$1 archive"
}

# stays reports whether the update site's install.sh names this release or a newer one, comparing as sort -V does.
stays() {
	[ -e "$site/install.sh" ] || return 1
	published=$(sed -n 's/^version=//p' "$site/install.sh")
	release_version "$published" "the update site's install.sh"
	newest=$(printf '%s\n%s\n' "$published" "$version" | sort -V | tail -n 1)
	[ "$newest" = "$published" ]
}

# stamp writes the script into the work folder with the release's version and Linux checksums on its unstamped lines.
stamp() {
	awk -v version="$version" -v amd64="$amd64" -v arm64="$arm64" '
		BEGIN {
			stamps["version=unstamped"] = "version=" version
			stamps["sha256_amd64=unstamped"] = "sha256_amd64=" amd64
			stamps["sha256_arm64=unstamped"] = "sha256_arm64=" arm64
		}
		($0 in stamps) {
			seen[$0]++
			$0 = stamps[$0]
		}
		{ print }
		END {
			for (line in stamps) {
				if (seen[line] != 1) {
					exit 1
				}
			}
		}
	' "$1" > "$work/install.sh" ||
		stop "$1 must hold each of version=unstamped, sha256_amd64=unstamped and sha256_arm64=unstamped once"
}

# sign signs the stamped install.sh in the work folder and checks its signature under the public key.
sign() {
	"$tool" sign -key "$key" "$work/install.sh" > /dev/null ||
		stop "releasetool could not sign install.sh, so the update site stays as it is"
	"$tool" verify -public "$public" "$work/install.sh" > /dev/null ||
		stop "releasetool could not verify the signed install.sh under the public key," \
			"so the update site stays as it is"
}

# trusted stops unless the release key or the standby that the script names signed the release record.
trusted() {
	for name in release_key standby_key; do
		printf '%s\n' '-----BEGIN PUBLIC KEY-----' "$(sed -n "s/^$name=//p" "$1")" '-----END PUBLIC KEY-----' \
			> "$work/$name.pem"
		"$tool" verify -public "$work/$name.pem" "$record" > /dev/null 2>&1 && return 0
	done
	stop "neither the release key nor the standby that $1 names signed the release record," \
		"so the update site stays as it is"
}

# publish stamps and signs install.sh in a work folder outside the update site, then moves both files into the site.
publish() {
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	stamp "$(dirname "$0")/install.sh"
	sign
	trusted "$(dirname "$0")/install.sh"
	mv "$work/install.sh.sig" "$site/install.sh.sig"
	mv "$work/install.sh" "$site/install.sh"
	say "install.sh now names Gophenberg $version"
}

# main signs install.sh, stamped from the checked release record, into the update site when the release is newer.
main() {
	[ "$#" = 5 ] || stop "usage: publish-install.sh <releasetool> <release record> <update site folder>" \
		"<signing key file> <public key>"
	tool=$1
	record=$2
	site=$3
	key=$4
	public=$5
	"$tool" verify -public "$public" "$record" > /dev/null ||
		stop "releasetool could not verify the release record under the public key," \
			"so the update site stays as it is"
	version=$(jq -r .version "$record")
	release_version "$version" "the release record"
	read_checksum amd64
	amd64=$sum
	read_checksum arm64
	arm64=$sum
	if stays; then
		say "the update site's install.sh names $published, which is not older than $version, so it stays as it is"
	else
		publish
	fi
}

main "$@"
