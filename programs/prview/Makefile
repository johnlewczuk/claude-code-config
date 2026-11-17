.PHONY: all web build test install clean

all: build

web/node_modules: web/package.json
	cd web && npm install
	touch web/node_modules

web: web/node_modules
	cd web && npm run build

build: web
	go build -o prview ./cmd/prview

test:
	go test ./...
	cd web && npm run check && npm test

install: web
	go install ./cmd/prview
