# tcp2rdp-web — build the embedded frontend, then the server binary.
# Requires: Go 1.22+, Node 18+ (with npm).

VERSION ?= 1.0.0
LDFLAGS := -s -w -X main.appVersion=$(VERSION)

.PHONY: all frontend build dist clean

frontend:
	cd frontend && npm install --no-audit --no-fund && npm run build

# 本机构建（Windows / Linux / macOS 通用，纯 Go 无 CGO）
build: frontend
	go build -trimpath -ldflags "$(LDFLAGS)" -o tcp2rdp-web$(shell go env GOEXE) .

# 常用发布包，输出到 dist/
dist: frontend
	mkdir -p dist
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/tcp2rdp-web-windows-amd64.exe .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/tcp2rdp-web-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/tcp2rdp-web-linux-arm64 .
	cd dist && sha256sum * > checksums.txt 2>/dev/null || true

clean:
	rm -rf dist tcp2rdp-web tcp2rdp-web.exe frontend/dist frontend/node_modules data
