#!/bin/sh
# Builds static binaries in the official Go container (no local Go needed).
#   ./build.sh            linux/amd64 (server)
#   ./build.sh all        linux/amd64, linux/arm64, darwin/arm64
set -e
cd "$(dirname "$0")"
VERSION=$(sed -n 's/^var version = "\(.*\)"/\1/p' cmd/image-watermark/main.go)
TARGETS="linux/amd64"
[ "$1" = "all" ] && TARGETS="linux/amd64 linux/arm64 darwin/arm64"
docker run --rm -v "$PWD":/src -w /src -v image-watermark-gomod:/go/pkg/mod golang:1.24-bookworm sh -c "
	set -e
	test -z \"\$(gofmt -l .)\" || { gofmt -l .; echo 'run gofmt'; exit 1; }
	go vet ./...
	go test ./...
	for t in $TARGETS; do
		os=\${t%/*}; arch=\${t#*/}
		CGO_ENABLED=0 GOOS=\$os GOARCH=\$arch go build -trimpath -ldflags='-s -w' \
			-o dist/image-watermark-\$os-\$arch ./cmd/image-watermark
		echo built dist/image-watermark-\$os-\$arch
	done
"
(cd dist && shasum -a 256 image-watermark-* > SHA256SUMS)
echo "version $VERSION"
