.PHONY: test race vet build

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build -trimpath -ldflags "-s -w" -o compatprobe ./cmd/compatprobe
