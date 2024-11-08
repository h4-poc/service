GO=go
GOFLAGS=-v

VERSION := $(shell git describe --tags --always)
BUILD_DATE := $(shell date -u '+%H:%M:%S@%Y-%m-%d')
GIT_COMMIT := $(shell git rev-parse HEAD)
INSTALLATION_MANIFESTS_URL := "github.com/h4-poc/service/manifests/base"
INSTALLATION_MANIFESTS_INSECURE_URL := "github.com/h4-poc/service/manifests/insecure" # ?ref=$(VERSION)

LDFLAGS=-ldflags "-X 'github.com/h4-poc/service/pkg/store.version=${VERSION}' \
				-X 'github.com/h4-poc/service/pkg/store.buildDate=${BUILD_DATE}' \
				-X 'github.com/h4-poc/service/pkg/store.gitCommit=${GIT_COMMIT}' \
				-X 'github.com/h4-poc/service/pkg/store.installationManifestsURL=${INSTALLATION_MANIFESTS_URL}' \
				-X 'github.com/h4-poc/service/pkg/store.installationManifestsInsecureURL=${INSTALLATION_MANIFESTS_INSECURE_URL}' "

# Default target
.DEFAULT_GOAL := build

# Build application
.PHONY: build
build: build-service build-supervisor

build-service:
	$(GO) build $(GOFLAGS) -o output/service $(LDFLAGS) cmd/service/service.go

build-supervisor:
	$(GO) build $(GOFLAGS) -o output/supervisor $(LDFLAGS) cmd/supervisor/supervisor.go

# Run application
.PHONY: run
run: build
	./$(BINARY_NAME) run

# Run tests
.PHONY: test
test:
	$(GO) test ./...

# Clean build artifacts
.PHONY: clean
clean:
	$(GO) clean
	rm -f $(BINARY_NAME)

# Execute all operations
.PHONY: all
all: clean build test

# Generate Swagger documentation (if using swag)
.PHONY: swagger
swagger:
	swag init -g $(MAIN_FILE) -o ./docs/swagger

# Format code
.PHONY: fmt
fmt:
	$(GO) fmt ./...

# Check code style
.PHONY: lint
lint:
	golangci-lint run

# Helm deployment
.PHONY: deploy
deploy:
	helm upgrade --install application-api ./deploy/application-api

.PHONY: codegen
codegen: $(GOBIN)/mockgen
	rm -f docs/commands/*
	go generate ./...

$(GOBIN)/mockgen:
	@go install github.com/golang/mock/mockgen@v1.6.0
	@mockgen -version

$(GOBIN)/golangci-lint:
	@mkdir dist || true
	@echo installing: golangci-lint
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOBIN) v1.55.2

# Help information
.PHONY: help
help:
	@echo "Usage:"
	@echo "  make build    - Build the application"
	@echo "  make run      - Run the application"
	@echo "  make test     - Run tests"
	@echo "  make clean    - Clean build artifacts"
	@echo "  make all      - Clean, build, and test"
