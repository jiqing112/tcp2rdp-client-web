# tcp2rdp-web — build the embedded frontend, then the server binary.
# Requires: Go 1.22+, Node 18+ (with npm).

VERSION ?= 1.1.0
LDFLAGS := -s -w -X main.appVersion=$(VERSION)

PLATFORMS := windows-amd64 linux-amd64 linux-arm64 darwin-amd64 darwin-arm64

.PHONY: all frontend build package dist clean

frontend:
	cd frontend && npm install --no-audit --no-fund && npm run build

# 本机构建（Windows / Linux / macOS 通用，纯 Go 无 CGO）
build: frontend
	go build -trimpath -ldflags "$(LDFLAGS)" -o tcp2rdp-web$(shell go env GOEXE) .

# 本机平台发布包：捆绑上游 jiqing112/tcp2rdp 最新 rdp2tcp，输出 dist/
package: frontend
	bash scripts/package.sh $(shell go env GOOS) $(shell go env GOARCH) $(VERSION) dist

# 全平台发布包（每个包都捆绑对应平台的上游最新 rdp2tcp），输出 dist/
dist: frontend
	@for t in $(PLATFORMS); do \
		bash scripts/package.sh "$${t%%-*}" "$${t#*-}" $(VERSION) dist; \
	done
	cd dist && sha256sum * > checksums.txt

clean:
	rm -rf dist tcp2rdp-web tcp2rdp-web.exe frontend/dist frontend/node_modules data
