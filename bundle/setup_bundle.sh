#!/usr/bin/env bash
# setup_bundle.sh — Creates a minimal OCI bundle for testing myruntime.
#
# This script:
#   1. Creates the bundle directory structure.
#   2. Extracts a minimal Alpine Linux rootfs (using busybox or Alpine mini).
#   3. Copies in the example config.json.
#
# USAGE:
#   sudo bash bundle/setup_bundle.sh [bundle-dir]
#
# REQUIRES: root (for mount namespace setup later), curl or wget.
#
# TESTED ON: Ubuntu 22.04, Kali Linux 2024

set -euo pipefail

BUNDLE_DIR="${1:-/tmp/myruntime-bundle}"
ROOTFS_DIR="${BUNDLE_DIR}/rootfs"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Setting up OCI bundle at: ${BUNDLE_DIR}"

# Create directory structure.
mkdir -p "${ROOTFS_DIR}"
mkdir -p "${BUNDLE_DIR}/overlay/lower"
mkdir -p "${BUNDLE_DIR}/overlay/upper"
mkdir -p "${BUNDLE_DIR}/overlay/work"
mkdir -p "${BUNDLE_DIR}/overlay/merged"

# Check if rootfs is already populated.
if [ -f "${ROOTFS_DIR}/bin/sh" ]; then
    echo "==> rootfs already populated at ${ROOTFS_DIR}, skipping extraction."
else
    echo "==> Downloading Alpine Linux mini rootfs..."
    # Alpine Linux provides a minimal rootfs suitable for containers.
    ALPINE_VERSION="3.19"
    ALPINE_ARCH="x86_64"
    ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/v${ALPINE_VERSION}/releases/${ALPINE_ARCH}/alpine-minirootfs-${ALPINE_VERSION}.0-${ALPINE_ARCH}.tar.gz"
    TARBALL="/tmp/alpine-minirootfs.tar.gz"

    if command -v curl &>/dev/null; then
        curl -L -o "${TARBALL}" "${ALPINE_URL}"
    elif command -v wget &>/dev/null; then
        wget -O "${TARBALL}" "${ALPINE_URL}"
    else
        echo "ERROR: curl or wget required to download rootfs." >&2
        exit 1
    fi

    echo "==> Extracting rootfs to ${ROOTFS_DIR}..."
    tar -xzf "${TARBALL}" -C "${ROOTFS_DIR}"
    rm -f "${TARBALL}"
    echo "==> rootfs extraction complete."
fi

# Copy example config.json into the bundle.
if [ -f "${SCRIPT_DIR}/example_config.json" ]; then
    cp "${SCRIPT_DIR}/example_config.json" "${BUNDLE_DIR}/config.json"
    echo "==> Copied config.json to ${BUNDLE_DIR}/config.json"
else
    echo "WARNING: ${SCRIPT_DIR}/example_config.json not found; skipping config copy." >&2
fi

# Verify the bundle.
echo ""
echo "==> Bundle layout:"
ls -la "${BUNDLE_DIR}/"
echo ""
echo "==> rootfs top-level:"
ls "${ROOTFS_DIR}/"

echo ""
echo "======================================================="
echo " Bundle ready at: ${BUNDLE_DIR}"
echo ""
echo " Build myruntime first (on Linux):"
echo "   go build -o myruntime ."
echo ""
echo " Then run:"
echo "   sudo ./myruntime create mybox --bundle ${BUNDLE_DIR}"
echo "   sudo ./myruntime start mybox"
echo "   sudo ./myruntime state mybox"
echo "   sudo ./myruntime kill mybox SIGTERM"
echo "   sudo ./myruntime delete mybox"
echo "======================================================="
