LINTER_BIN ?= golangci-lint

GO111MODULE := on
export GO111MODULE

.PHONY: build
build: bin/virtual-kubelet

.PHONY: clean
clean: files := bin/virtual-kubelet
clean:
	@rm $(files) &>/dev/null || exit 0

.PHONY: test
test:
	@echo running tests
	@go test -v ./...

# The end-to-end test creates a VPC, an IAM role, a log group and an ECS cluster in the AWS account of the credentials
# it is run with, and deletes them when it ends. Set VK_TEST_FARGATE_REGION to choose the Region (default us-east-1).
.PHONY: test-e2e
test-e2e:
	@echo running end-to-end tests
	@go test -v -tags e2e -timeout 30m .

.PHONY: vet
vet:
	@go vet ./... #$(packages)

.PHONY: lint
lint:
	@$(LINTER_BIN) run --new-from-rev "HEAD~$(git rev-list master.. --count)" ./...

.PHONY: check-mod
check-mod: # verifies that module changes for go.mod and go.sum are checked in
	@hack/ci/check_mods.sh

.PHONY: mod
mod:
	@go mod tidy

bin/virtual-kubelet: BUILD_VERSION          ?= $(shell git describe --tags --always --dirty="-dev")
bin/virtual-kubelet: BUILD_DATE             ?= $(shell date -u '+%Y-%m-%d-%H:%M UTC')
bin/virtual-kubelet: VERSION_FLAGS    := -ldflags='-X "main.buildVersion=$(BUILD_VERSION)" -X "main.buildTime=$(BUILD_DATE)"'

bin/%:
	CGO_ENABLED=0 go build -ldflags '-extldflags "-static"' -o bin/$(*) $(VERSION_FLAGS) ./cmd/$(*)
