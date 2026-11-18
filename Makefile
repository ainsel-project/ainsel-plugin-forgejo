IMAGE ?= ghcr.io/ainsel-project/ainsel-plugin-forgejo

.PHONY: test ui build image chart-lint run

## test: run the Go tests and the UI tests
test:
	go test ./...
	pnpm -C ui test

## ui: install UI dependencies and build the bundle
ui:
	pnpm -C ui install --frozen-lockfile && pnpm -C ui build

## build: build the plugin binary (needs the UI bundle embedded)
build: ui
	go build -o bin/plugin ./cmd/plugin

## image: build the container image
image:
	docker build -t $(IMAGE):dev .

## chart-lint: lint the Helm chart and check its render tests
chart-lint:
	bash chart/tests/render_test.sh

## run: run the plugin locally
run:
	go run ./cmd/plugin
