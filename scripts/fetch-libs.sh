#!/usr/bin/env bash
# fetch-libs.sh — download prebuilt vastlint-ffi static libraries from a
# GitHub Release and unpack them into the correct libs/ subdirectories.
#
# Usage:
#   ./scripts/fetch-libs.sh v0.1.0
#
# The script downloads the four platform tarballs produced by the vastlint
# monorepo release workflow:
#   vastlint-ffi-darwin-aarch64.tar.gz  → libs/darwin_arm64/libvastlint.a
#   vastlint-ffi-darwin-x86_64.tar.gz   → libs/darwin_amd64/libvastlint.a
#   vastlint-ffi-linux-aarch64.tar.gz   → libs/linux_arm64/libvastlint.a
#   vastlint-ffi-linux-x86_64.tar.gz    → libs/linux_amd64/libvastlint.a
#
# It also copies vastlint.h from any of the tarballs (the header is identical
# across platforms) into the repo root.
#
# Requirements: curl, tar. No other dependencies.

set -euo pipefail

REPO="aleksUIX/vastlint"
RELEASE_TAG="${1:-}"

if [[ -z "$RELEASE_TAG" ]]; then
  echo "Usage: $0 <release-tag>  (e.g. v0.1.0)" >&2
  exit 1
fi

BASE_URL="https://github.com/${REPO}/releases/download/${RELEASE_TAG}"

# Map: tarball name → target libs/ subdirectory
declare -A PLATFORM_MAP=(
  ["vastlint-ffi-macos-aarch64.tar.gz"]="darwin_arm64"
  ["vastlint-ffi-macos-x86_64.tar.gz"]="darwin_amd64"
  ["vastlint-ffi-linux-aarch64.tar.gz"]="linux_arm64"
  ["vastlint-ffi-linux-x86_64.tar.gz"]="linux_amd64"
)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

HEADER_COPIED=0

for TARBALL in "${!PLATFORM_MAP[@]}"; do
  PLATFORM_DIR="${PLATFORM_MAP[$TARBALL]}"
  URL="${BASE_URL}/${TARBALL}"
  DEST="${REPO_ROOT}/libs/${PLATFORM_DIR}"
  UNPACK_DIR="${TMPDIR}/${PLATFORM_DIR}"

  echo "→ Downloading ${TARBALL} ..."
  mkdir -p "$UNPACK_DIR"
  if ! curl -fsSL "$URL" -o "${TMPDIR}/${TARBALL}"; then
    echo "  WARNING: failed to download ${URL} — skipping" >&2
    continue
  fi

  tar xzf "${TMPDIR}/${TARBALL}" -C "$UNPACK_DIR"

  mkdir -p "$DEST"
  cp "${UNPACK_DIR}/libvastlint.a" "${DEST}/libvastlint.a"
  echo "  ✓ ${DEST}/libvastlint.a"

  # Copy the header once — it's the same across all platforms.
  if [[ $HEADER_COPIED -eq 0 && -f "${UNPACK_DIR}/vastlint.h" ]]; then
    cp "${UNPACK_DIR}/vastlint.h" "${REPO_ROOT}/vastlint.h"
    echo "  ✓ vastlint.h"
    HEADER_COPIED=1
  fi
done

echo ""
echo "Done. Libraries updated to ${RELEASE_TAG}."
echo "Commit the changes to libs/ and vastlint.h, then tag this repo at ${RELEASE_TAG}."
