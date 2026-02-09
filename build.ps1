# --------------------------------------------
# build.ps1 - Build Go project with bimg/libvips
# --------------------------------------------

# ----------------------
# Paths (tùy chỉnh)
# ----------------------
$VIPS_ROOT = "D:\work\A06\Image_Resize\vips-dev-8.10"
$MSYS_MINGW_BIN = "C:\msys64\mingw64\bin"
$PROJECT_DIR = "D:\work\A06\Image_Resize"
$OUTPUT_EXE = "$PROJECT_DIR\Resize_tool.exe"

# ----------------------
# Set environment variables
# ----------------------
Write-Host "[INFO] Setting environment variables..."

$env:PKG_CONFIG_PATH = "$VIPS_ROOT\lib\pkgconfig"
$env:CGO_LDFLAGS = "-L$VIPS_ROOT\lib"
$env:CGO_CFLAGS = "-I$VIPS_ROOT\include"
$env:PATH = "$MSYS_MINGW_BIN;" + $env:PATH

# Enable cgo
go env -w CGO_ENABLED=1
go env -w CC=x86_64-w64-mingw32-gcc

# ----------------------
# Clean previous builds
# ----------------------
Write-Host "[INFO] Cleaning previous builds..."
cd $PROJECT_DIR
go clean -cache
go mod tidy

# ----------------------
# Build project
# ----------------------
Write-Host "[INFO] Building project with optimization flags..."
# ĐÃ THÊM: -ldflags "-s -w"
go build -v -ldflags "-s -w" -o $OUTPUT_EXE

if ($LASTEXITCODE -eq 0) {
    Write-Host "[SUCCESS] Build completed: $OUTPUT_EXE"
} else {
    Write-Host "[ERROR] Build failed."
}