.PHONY: build web test check dev serve docker

web:
	npm --prefix web ci
	npm --prefix web run build

build: web
	mkdir -p bin
	go build -trimpath -ldflags='-s -w' -o bin/gpup ./cmd/gpup

test: web
	go test ./...
	npm --prefix web test

check: web
	go vet ./...
	go test -race ./...
	npm --prefix web test

dev:
	npm --prefix web run dev

serve: build
	./bin/gpup serve

docker:
	docker build -f deploy/docker/Dockerfile -t gpup:local .
