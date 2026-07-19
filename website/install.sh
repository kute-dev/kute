#!/bin/sh
set -eu

repo="kute-dev/kute"
bin="kute"

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

main() {
	version="${KUTE_VERSION:-latest}"
	install_dir="${KUTE_INSTALL_DIR:-/usr/local/bin}"

	need curl
	need tar

	case "$(uname -s)" in
		Linux) os="linux" ;;
		Darwin) os="darwin" ;;
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

	(
		cd "$tmp"
		if command -v sha256sum >/dev/null 2>&1; then
			grep -F "  ${archive}" checksums.txt | sha256sum -c - >/dev/null || fail "checksum verification failed"
		elif command -v shasum >/dev/null 2>&1; then
			grep -F "  ${archive}" checksums.txt | shasum -a 256 -c - >/dev/null || fail "checksum verification failed"
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
