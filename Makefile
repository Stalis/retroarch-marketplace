.PHONY: run build test e2e vet fmt admin-install admin-build admin-dev admin-test

run:
	go run . $(ARGS)

build:
	mkdir -p bin
	go build -o bin/catalog-server .

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

admin-test:
	npm test --prefix web
