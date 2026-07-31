#!/bin/sh
set -eu

repo="kute-dev/kute"
bin="kute"
verify_docs="https://kute.dev/verify.html"

# The keyless signing identity is the release workflow itself. Matched by
# regexp rather than by exact tag so this script never needs to know which
# version it just fetched; the anchor is what keeps it to that one workflow
# in that one repo.
cert_identity="^https://github\.com/${repo}/\.github/workflows/release\.yml@refs/tags/"
cert_issuer="https://token.actions.githubusercontent.com"

fail() {
	printf 'kute install: %s\n' "$1" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

cleanup() {
	[ -n "${tmp:-}" ] && rm -rf "$tmp"
}

# The checksum verified below proves the archive matches a manifest that came
# down the same wire as the archive; only the signature says the release is
# ours. cosign is not a dependency of this script, so a machine without it
# gets a note and the checksum — a stronger check that nobody can run is not
# stronger. Same for a release predating signing: don't fail an install that
# used to work.
verify_signature() {
	if ! command -v cosign >/dev/null 2>&1; then
		printf 'note: cosign not found; verified checksum only.\n'
		printf '      %s explains how to check the signature.\n' "$verify_docs"
		return 0
	fi

	bundle="${tmp}/${archive}.sigstore.json"
	if ! curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$bundle" "${base_url}/${archive}.sigstore.json" 2>/dev/null; then
		printf 'note: %s publishes no signature; verified checksum only.\n' "$version"
		return 0
	fi

	# cosign 3 reads the bundle format by default and has no
	# --new-bundle-format flag; cosign 2 needs it and rejects a bundle
	# without it. Rather than parse `cosign version`, try the modern form and
	# fall back — a bundle that is genuinely bad fails both ways, so the
	# retry can only rescue an old cosign, never hide a bad signature.
	if verify_blob_with "$bundle" || verify_blob_with "$bundle" --new-bundle-format; then
		printf 'Verified signature (cosign, keyless).\n'
		return 0
	fi

	fail "signature verification failed for ${archive} — do not run it; see ${verify_docs}"
}

verify_blob_with() {
	bundle="$1"
	shift
	cosign verify-blob \
		--certificate-identity-regexp "$cert_identity" \
		--certificate-oidc-issuer "$cert_issuer" \
		--bundle "$bundle" \
		"$@" \
		"${tmp}/${archive}" >/dev/null 2>&1
}

main() {
	version="${KUTE_VERSION:-latest}"
	install_dir="${KUTE_INSTALL_DIR:-/usr/local/bin}"

	need curl
	need tar

	case "$(uname -s)" in
		Linux) os="linux" ;;
		Darwin) os="darwin" ;;
		# Git Bash, MSYS2 and Cygwin are where a Windows user lands after
		# copying the curl one-liner off the website. There is a Windows
		# build; it just isn't installed from here.
		MINGW*|MSYS*|CYGWIN*)
			fail "Windows is not installed from this script. Use PowerShell:
    irm https://kute.dev/install.ps1 | iex
  or Scoop:
    scoop bucket add kute-dev https://github.com/kute-dev/scoop-bucket
    scoop install kute"
			;;
		*) fail "unsupported OS: $(uname -s)" ;;
	esac

	case "$(uname -m)" in
		x86_64|amd64) arch="amd64" ;;
		arm64|aarch64) arch="arm64" ;;
		*) fail "unsupported architecture: $(uname -m)" ;;
	esac

	# uname -m lies under Rosetta; ask the kernel if we are translated.
	if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = "1" ]; then
		arch="arm64"
	fi

	if [ "$version" = "latest" ]; then
		printf 'Resolving latest kute release...\n'
		latest_url="https://github.com/${repo}/releases/latest"
		version="$(curl -fsSLI --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' "$latest_url" | sed 's#.*/##')"
	fi

	case "$version" in
		v[0-9]*) ;;
		[0-9]*) version="v${version}" ;;
		*) fail "could not resolve release version (got: ${version:-nothing})" ;;
	esac

	archive_version="${version#v}"
	archive="kute_${archive_version}_${os}_${arch}.tar.gz"
	base_url="https://github.com/${repo}/releases/download/${version}"
	tmp="$(mktemp -d 2>/dev/null || mktemp -d -t kute-install)"
	trap cleanup EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM

	printf 'Installing kute %s for %s/%s...\n' "$version" "$os" "$arch"

	curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "${tmp}/${archive}" "${base_url}/${archive}" || fail "download failed: ${base_url}/${archive}"
	curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "${tmp}/checksums.txt" "${base_url}/checksums.txt" || fail "download failed: ${base_url}/checksums.txt"

	verify_signature

	(
		cd "$tmp"
		# Exact filename match on field 2. checksums.txt lists every release
		# artifact, so a substring match for kute_X_linux_amd64.tar.gz would
		# also pull in the .sigstore.json/.sbom.json lines and ask sha256sum
		# to verify files that aren't here. awk compares strings rather than
		# patterns, so the name's dots can't act as wildcards either; the
		# leading '*' is sha256sum's binary-mode marker, which install.ps1
		# tolerates the same way.
		select_sum() { awk -v a="$archive" '$2 == a || $2 == "*" a' checksums.txt; }
		[ -n "$(select_sum)" ] || fail "no checksum entry for ${archive}"
		if command -v sha256sum >/dev/null 2>&1; then
			select_sum | sha256sum -c - >/dev/null || fail "checksum verification failed"
		elif command -v shasum >/dev/null 2>&1; then
			select_sum | shasum -a 256 -c - >/dev/null || fail "checksum verification failed"
		else
			fail "missing required command: sha256sum or shasum"
		fi

		tar -xzf "$archive"
	)

	[ -x "${tmp}/${bin}" ] || fail "archive did not contain executable: ${bin}"

	if [ ! -d "$install_dir" ]; then
		if mkdir -p "$install_dir" 2>/dev/null; then
			:
		elif command -v sudo >/dev/null 2>&1; then
			printf 'Elevating with sudo to create %s\n' "$install_dir"
			sudo mkdir -p "$install_dir"
		else
			fail "could not create install dir: ${install_dir}"
		fi
	fi

	# Stage next to the destination and mv over it: atomic, and replacing
	# the inode avoids ETXTBSY when the old binary is still running.
	if [ -w "$install_dir" ]; then
		cp "${tmp}/${bin}" "${install_dir}/${bin}.new"
		chmod 755 "${install_dir}/${bin}.new"
		mv "${install_dir}/${bin}.new" "${install_dir}/${bin}"
	elif command -v sudo >/dev/null 2>&1; then
		printf 'Elevating with sudo to write %s\n' "$install_dir"
		sudo cp "${tmp}/${bin}" "${install_dir}/${bin}.new"
		sudo chmod 755 "${install_dir}/${bin}.new"
		sudo mv "${install_dir}/${bin}.new" "${install_dir}/${bin}"
	else
		fail "install dir is not writable and sudo is unavailable: ${install_dir}"
	fi

	printf 'kute installed to %s\n' "${install_dir}/${bin}"
	"${install_dir}/${bin}" --version 2>/dev/null || true

	case ":${PATH}:" in
		*":${install_dir}:"*) ;;
		*) printf 'note: %s is not in your PATH\n' "$install_dir" ;;
	esac
}

main "$@"
