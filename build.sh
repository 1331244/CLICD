#!/bin/bash
set -e

# CLICD Build Script
# Builds frontend and backend into a single deployable package

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="$SCRIPT_DIR/build"
DIST_DIR="$SCRIPT_DIR/dist"
FRONTEND_DIR="$SCRIPT_DIR/frontend"
BACKEND_DIR="$SCRIPT_DIR/backend"
WEB_DIR="$SCRIPT_DIR/web"
EMBED_WEB_DIR="$BACKEND_DIR/internal/server/web"

echo "====================================="
echo "  CLICD Build Script"
echo "====================================="

# Clean previous build
rm -rf "$BUILD_DIR"
rm -rf "$DIST_DIR"
rm -rf "$WEB_DIR"
rm -rf "$EMBED_WEB_DIR"
mkdir -p "$BUILD_DIR"
mkdir -p "$DIST_DIR"
mkdir -p "$WEB_DIR"
mkdir -p "$EMBED_WEB_DIR"
touch "$EMBED_WEB_DIR/.gitkeep"

# Step 1: Build frontend
echo ""
echo "[1/3] Building frontend..."
cd "$FRONTEND_DIR"

if [ ! -d "node_modules" ]; then
    echo "Installing frontend dependencies..."
    npm install
fi

npm run build

# Copy frontend build to web directory (for Go embed)
cp -r dist/* "$WEB_DIR/"
# Keep the Go embed directory in sync with the frontend build.
cp -r dist/* "$EMBED_WEB_DIR/"
touch "$EMBED_WEB_DIR/.gitkeep"
echo "Frontend built successfully"

# Step 2: Build Go backend
echo ""
echo "[2/3] Building Go backend..."
cd "$BACKEND_DIR"

go mod tidy
go mod download

BUILD_VERSION="${CLICD_VERSION:-dev}"
BUILD_REVISION="${CLICD_REVISION:-$(git -C "$SCRIPT_DIR" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)}"
TARGET_GOOS="${CLICD_GOOS:-linux}"
TARGET_GOARCH="${CLICD_GOARCH:-amd64}"

case "$TARGET_GOARCH" in
    all) TARGET_GOARCH_LIST="amd64 arm64" ;;
    amd64|arm64) TARGET_GOARCH_LIST="$TARGET_GOARCH" ;;
    *)
        echo "Unsupported CLICD_GOARCH: $TARGET_GOARCH (expected amd64, arm64, or all)" >&2
        exit 2
        ;;
esac

for arch in $TARGET_GOARCH_LIST; do
    echo "Target: ${TARGET_GOOS}/${arch}"
    GOOS="$TARGET_GOOS" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X clicd/internal/version.Version=${BUILD_VERSION} -X clicd/internal/version.Revision=${BUILD_REVISION}" -o "$BUILD_DIR/clicd-linux-${arch}" .
done

first_arch="${TARGET_GOARCH_LIST%% *}"
cp "$BUILD_DIR/clicd-linux-${first_arch}" "$BUILD_DIR/clicd"

echo "Go backend built successfully"

# Step 3: Package
echo ""
echo "[3/3] Packaging..."
cp -r "$WEB_DIR" "$BUILD_DIR/web"
cp "$SCRIPT_DIR/install.sh" "$BUILD_DIR/install.sh" 2>/dev/null || true
chmod +x "$BUILD_DIR"/clicd*

for arch in $TARGET_GOARCH_LIST; do
    asset_dir="clicd-linux-${arch}"
    package_root="$BUILD_DIR/package-${arch}"
    rm -rf "$package_root"
    mkdir -p "$package_root/$asset_dir"
    cp "$BUILD_DIR/clicd-linux-${arch}" "$package_root/$asset_dir/clicd"
    cp "$BUILD_DIR/install.sh" "$package_root/$asset_dir/install.sh" 2>/dev/null || true
    chmod +x "$package_root/$asset_dir/clicd"
    [ ! -f "$package_root/$asset_dir/install.sh" ] || chmod +x "$package_root/$asset_dir/install.sh"
    tar -C "$package_root" -czf "$DIST_DIR/${asset_dir}.tar.gz" "$asset_dir"
    cp "$BUILD_DIR/clicd-linux-${arch}" "$DIST_DIR/${asset_dir}"
done

echo ""
echo "====================================="
echo "  Build Complete!"
echo "====================================="
echo "  Output: $BUILD_DIR/clicd"
echo "  Web:    $BUILD_DIR/web/"
echo "  Dist:   $DIST_DIR/"
for arch in $TARGET_GOARCH_LIST; do
    echo "          dist/clicd-linux-${arch}"
    echo "          dist/clicd-linux-${arch}.tar.gz"
done
echo ""
echo "  To deploy:"
echo "    1. Copy build/ directory to server"
echo "    2. Run: ./clicd server"
echo "====================================="
