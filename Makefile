VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet run release clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/appanel ./cmd/appanel

test:
	go test ./...

vet:
	gofmt -l . | (! grep .) && go vet ./...

run: build
	./bin/appanel serve

# Static binaries + checksums, the layout install.sh expects.
release: clean
	mkdir -p dist
	for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/appanel ./cmd/appanel && \
	  tar -czf dist/appanel-linux-$$arch.tar.gz -C dist appanel && rm dist/appanel; \
	done
	cd dist && sha256sum appanel-linux-*.tar.gz > checksums.txt

clean:
	rm -rf bin dist
