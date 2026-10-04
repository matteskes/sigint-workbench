#!/bin/bash
# Fetch the ONNX Runtime shared library for this platform into .ort/ and
# print the ORT_LIBRARY_PATH export line. Same release CI
# (.github/workflows/ci.yml) and docker/Dockerfile.classifier use.
# Idempotent: skips the download when the library already exists, and
# short-circuits entirely when $ORT_LIBRARY_PATH already points at a
# file.
#
# Usage:
#   make ort-lib                      # or: ./scripts/fetch-ort.sh
#   eval "$(./scripts/fetch-ort.sh)"  # export ORT_LIBRARY_PATH here
#
# Env: ORT_VERSION (default 1.29.0, must be >= 1.29), ORT_DIR (default
# .ort).
set -euo pipefail

cd "$(dirname "$0")/.."

ORT_VERSION="${ORT_VERSION:-1.29.0}"
ORT_DIR="${ORT_DIR:-.ort}"

if [ -n "${ORT_LIBRARY_PATH:-}" ] && [ -f "${ORT_LIBRARY_PATH}" ]; then
	echo "export ORT_LIBRARY_PATH=\"${ORT_LIBRARY_PATH}\""
	exit 0
fi

GOOS="$(go env GOOS 2>/dev/null || true)"
GOARCH="$(go env GOARCH 2>/dev/null || true)"
case "${GOOS}/${GOARCH}" in
	darwin/arm64)  pkg="onnxruntime-osx-arm64";     lib="libonnxruntime.dylib" ;;
	darwin/amd64)  pkg="onnxruntime-osx-x64";       lib="libonnxruntime.dylib" ;;
	linux/amd64)   pkg="onnxruntime-linux-x64";     lib="libonnxruntime.so" ;;
	linux/arm64)   pkg="onnxruntime-linux-aarch64"; lib="libonnxruntime.so" ;;
	*)
		echo "fetch-ort: unsupported platform ${GOOS:-?}/${GOARCH:-?} —" \
			"install ONNX Runtime >= 1.29 yourself and set ORT_LIBRARY_PATH" >&2
		exit 1
		;;
esac

dest="${ORT_DIR}/${pkg}-${ORT_VERSION}"
libpath="${dest}/lib/${lib}"
if [ ! -f "${libpath}" ]; then
	mkdir -p "${ORT_DIR}"
	echo "fetch-ort: downloading ONNX Runtime ${ORT_VERSION} (${pkg})..." >&2
	tgz="${ORT_DIR}/${pkg}-${ORT_VERSION}.tgz"
	curl -fL --retry 3 -o "${tgz}" \
		"https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${pkg}-${ORT_VERSION}.tgz"
	tar -xzf "${tgz}" -C "${ORT_DIR}"
	rm -f "${tgz}"
fi
if [ ! -f "${libpath}" ]; then
	echo "fetch-ort: library missing after download: ${libpath}" >&2
	exit 1
fi
echo "export ORT_LIBRARY_PATH=\"$(pwd)/${libpath}\""