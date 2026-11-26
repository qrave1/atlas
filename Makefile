.PHONY: test check build
test:
	go test ./...
check:
	go vet ./...
build:
	go build -o bin/atlas ./cmd/atlas
