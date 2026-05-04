.PHONY: build build-all clean test help release

BIN := plan-reviewer
LDFLAGS := -ldflags='-s -w' -trimpath
CMD := ./cmd/plan-reviewer
PLATFORMS := darwin-arm64 darwin-amd64 linux-amd64 linux-arm64

help:
	@echo "make build         - build for the current platform (uncompressed) into bin/"
	@echo "make build-all     - cross-compile all platforms and write only bin/<os>-<arch>/plan-reviewer.gz"
	@echo "make test          - run tests"
	@echo "make clean         - remove bin/"

build:
	@OS=$$(go env GOOS); ARCH=$$(go env GOARCH); \
		mkdir -p bin/$$OS-$$ARCH; \
		go build $(LDFLAGS) -o bin/$$OS-$$ARCH/$(BIN) $(CMD)

# Cross-compile for all four platforms, gzip each, and remove the uncompressed
# output. This is the shape we ship via GitHub — the .gz files are the only
# binary artifacts committed.
build-all:
	@for p in $(PLATFORMS); do \
		OS=$${p%-*}; ARCH=$${p#*-}; \
		mkdir -p bin/$$p; \
		echo "building $$p"; \
		GOOS=$$OS GOARCH=$$ARCH go build $(LDFLAGS) -o bin/$$p/$(BIN) $(CMD); \
		gzip -9 -f bin/$$p/$(BIN); \
	done
	@ls -lh bin/*/$(BIN).gz

test:
	go test ./...

clean:
	rm -rf bin/
