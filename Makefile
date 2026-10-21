GO_FILES = $(shell find . -path './.*' -prune -o -name '*.go' -not -name '*_test.go' -print) go.mod go.sum

all: \
  bin/kustomize-plugin-resourceinjector \
  bin/kustomize-plugin-vendorkustomization \
  bin/kustomize-plugin-yqtransform

bin/kustomize-plugin-%: $(GO_FILES)
	go build -o $@ ./cmd/$*

test: \
	test/unit \
	test/kustomize-plugin-resourceinjector \
	test/kustomize-plugin-vendorkustomization \
	test/kustomize-plugin-yqtransform

.PHONY: test/unit
test/unit: $(GO_FILES)
	go test ./cmd/... ./internal/...

.PHONY: test/%
test/kustomize-plugin-%: bin/kustomize-plugin-%
	go test -v ./test/$*/...
