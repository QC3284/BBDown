.PHONY: build run test clean proto

APP := BBDown
SRC := ./cmd/bbdown

build:
	go build -ldflags="-s -w" -o bin/$(APP) $(SRC)

run:
	go run $(SRC)

test:
	go test ./...

clean:
	rm -rf bin/

proto:
	cd internal/drm/proto && protoc --go_out=. --go_opt=paths=source_relative *.proto
