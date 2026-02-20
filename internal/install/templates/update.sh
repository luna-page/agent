#!/bin/bash

set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
	echo "This script must be run as root (use sudo)."
	exit 1
fi

TARGET_BINARY="{{ .BinaryPath }}"
SERVICE_NAME="{{ .ServiceName }}"
RELEASES_URL="https://github.com/luna-page/agent/releases/latest/download"

usage() {
	echo "Usage:"
	echo "  sudo {{ .UpdateScriptPath }}"
	echo "  sudo {{ .UpdateScriptPath }} /path/to/new/agent/binary"
}

if [[ "$#" -gt 1 ]]; then
	usage
	exit 1
fi

detect_release_arch() {
	case "$(uname -m)" in
		x86_64|amd64)
			echo "amd64"
			;;
		aarch64|arm64)
			echo "arm64"
			;;
		armv7l|armv7)
			echo "armv7"
			;;
		i386|i686)
			echo "386"
			;;
		*)
			echo ""
			;;
	esac
}

download_latest_binary() {
	local arch
	arch="$(detect_release_arch)"
	if [[ -z "${arch}" ]]; then
		echo "Unsupported architecture: $(uname -m)"
		exit 1
	fi

	if ! command -v tar >/dev/null 2>&1; then
		echo "Required command not found: tar"
		exit 1
	fi

	local tmp_dir archive_path download_url
	tmp_dir="$(mktemp -d)"
	archive_path="${tmp_dir}/agent.tar.gz"
	download_url="${RELEASES_URL}/agent-linux-${arch}.tar.gz"

	echo "Downloading latest release for ${arch}..."
	if command -v curl >/dev/null 2>&1; then
		curl -fL "${download_url}" -o "${archive_path}"
	elif command -v wget >/dev/null 2>&1; then
		wget -O "${archive_path}" "${download_url}"
	else
		echo "Neither curl nor wget is installed; cannot download update."
		rm -rf "${tmp_dir}"
		exit 1
	fi

	echo "Extracting archive..."
	tar -xzf "${archive_path}" -C "${tmp_dir}"

	if [[ ! -f "${tmp_dir}/agent" ]]; then
		echo "Downloaded archive did not contain an agent binary."
		rm -rf "${tmp_dir}"
		exit 1
	fi

	chmod +x "${tmp_dir}/agent"
	echo "${tmp_dir}/agent"
}

cleanup_temp_source=""
SOURCE_BINARY=""

if [[ "$#" -eq 1 ]]; then
	SOURCE_BINARY="$1"
else
	SOURCE_BINARY="$(download_latest_binary)"
	cleanup_temp_source="$(dirname "${SOURCE_BINARY}")"
fi

trap 'if [[ -n "${cleanup_temp_source}" ]]; then rm -rf "${cleanup_temp_source}"; fi' EXIT

if [[ ! -f "${SOURCE_BINARY}" ]]; then
	echo "File not found: ${SOURCE_BINARY}"
	exit 1
fi

if [[ ! -x "${SOURCE_BINARY}" ]]; then
	echo "The provided file is not executable: ${SOURCE_BINARY}"
	exit 1
fi

echo "Stopping service..."
systemctl stop "${SERVICE_NAME}"

echo "Updating binary..."
install -m 0755 "${SOURCE_BINARY}" "${TARGET_BINARY}"

echo "Starting service..."
systemctl start "${SERVICE_NAME}"

echo
echo "Update completed."
echo "Current service status:"
systemctl --no-pager --lines=5 status "${SERVICE_NAME}"
