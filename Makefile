VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 linux/arm darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build test install dist clean web

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/birdie ./cmd/birdie

test:
	go vet ./...
	go test -race ./...

install: build
	install -Dm755 bin/birdie $(HOME)/.local/bin/birdie

dist:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "dist/birdie-$$os-$$arch$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags '$(LDFLAGS)' -o dist/birdie-$$os-$$arch$$ext ./cmd/birdie || exit 1; \
	done
	cd dist && rm -f checksums.txt && sha256sum birdie-* > checksums.txt

# web builds the web app into internal/webui/dist, which the binary embeds.
# Without it, birdie serves a page saying the app isn't built in.
web:
	cd web && npm ci && npm run build

clean:
	rm -rf bin dist
