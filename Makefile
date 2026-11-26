.PHONY: test check build
test:
	go test ./...
check:
	go vet ./...
build:
	go build -o bin/atlas ./cmd/atlas
	go build -o bin/google-auth ./cmd/google-auth
