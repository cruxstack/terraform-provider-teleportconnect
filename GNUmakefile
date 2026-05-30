BINARY  := terraform-provider-teleportconnect
VERSION ?= dev
BUILD_DIR := build

GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: default
default: build

.PHONY: build
build:
	@mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .

.PHONY: install
install:
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" .

.PHONY: fmt
fmt:
	gofmt -s -w .

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: vulncheck
vulncheck:
	govulncheck ./...

# Unit tests only (no network / live cluster).
.PHONY: test
test:
	go test ./... -count=1

# Acceptance tests. Requires TF_ACC=1 plus the TC_* environment variables
# (see test/integration/README.md). These create real resources.
.PHONY: testacc
testacc:
	TF_ACC=1 go test ./internal/provider/... -v -count=1 -timeout 30m

# Spin up the local single-node Teleport cluster, run acceptance tests
# against it, then tear it down.
.PHONY: testacc-local
testacc-local:
	./test/integration/run.sh

# Regenerate registry documentation from the provider schema, the examples/
# directory, and the hand-written guides under templates/guides/. The
# underlying mechanism is `go generate ./...` (see the //go:generate directive
# in main.go).
.PHONY: docs
docs:
	go generate ./...

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) dist
