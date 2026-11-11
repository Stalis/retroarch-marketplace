.PHONY: run build build-release test e2e vet fmt admin-install admin-build admin-dev admin-dev-mock admin-mock admin-test
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")


run:
	go run . serve $(ARGS)

build:
	mkdir -p bin
	go build -ldflags="-X main.Version=$(VERSION)" -o bin/content-catalog .

build-release: admin-build
	mkdir -p bin
	go build -tags release -ldflags="-s -w -X main.Version=$(VERSION)" -o bin/content-catalog .

test:
	go test ./...

e2e:
	go test -count=1 -run '^TestE2E$$' -v .

vet:
	go vet ./...

fmt:
	go fmt ./...

admin-install:
	npm ci --prefix web

admin-build:
	npm run build --prefix web

admin-dev:
	npm run dev --prefix web

admin-dev-mock:
	npm run dev:mock --prefix web

admin-mock:
	npm run mock --prefix web

admin-test:
	npm test --prefix web
