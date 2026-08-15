.PHONY: build run test clean

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
