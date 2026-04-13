mkdir -p scripts

cat > scripts/build_release_artifacts.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./scripts/build_release_artifacts.sh v-0-1-0
# Optional env:
#   TARGETS="darwin-arm64 darwin-amd64 linux-amd64 linux-arm64"
#   OUT_DIR="./dist/v-0-1-0"
#   PKG="./cmd/accords-mcp"

VERSION_LABEL="${1:-v-0-1-0}"
TARGETS="${TARGETS:-darwin-arm64 darwin-amd64}"
OUT_DIR="${OUT_DIR:-./dist/${VERSION_LABEL}}"
PKG="${PKG:-./cmd/accords-mcp}"
BIN_NAME="accords-mcp"

if [[ ! -f "go.mod" ]]; then
  echo "error: run this from the accords-mcp repo root (go.mod not found)" >&2
  exit 1
fi

sha256_file() {
  local f="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$f" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$f" | awk '{print $1}'
  else
    echo "error: need shasum or sha256sum" >&2
    exit 1
  fi
}

mkdir -p "$OUT_DIR"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/accords-mcp-release.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT

for target in $TARGETS; do
  GOOS="${target%-*}"
  GOARCH="${target#*-}"

  STAGE_DIR="${TMP_ROOT}/${target}"
  BIN_PATH="${STAGE_DIR}/${BIN_NAME}"
  ARCHIVE_PATH="${OUT_DIR}/${BIN_NAME}-${target}.tar.gz"

  mkdir -p "$STAGE_DIR"

  echo "==> building ${BIN_NAME} for ${GOOS}/${GOARCH}"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags="-s -w" -o "$BIN_PATH" "$PKG"

  chmod 0755 "$BIN_PATH"

  echo "==> packaging ${ARCHIVE_PATH}"
  tar -C "$STAGE_DIR" -czf "$ARCHIVE_PATH" "$BIN_NAME"

  SHA="$(sha256_file "$ARCHIVE_PATH")"
  printf '%s\n' "$SHA" > "${ARCHIVE_PATH}.sha256"

  echo "    sha256: $SHA"
done

echo ""
echo "release artifacts written to: ${OUT_DIR}"
ls -lh "$OUT_DIR"
EOF

chmod +x scripts/build_release_artifacts.sh
./scripts/build_release_artifacts.sh v-0-1-0

