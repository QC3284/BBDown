.PHONY: build run test clean smoke

APP := BBDown
SRC := ./cmd/bbdown

build:
	go build -ldflags="-s -w" -o bin/$(APP) $(SRC)

run:
	go run $(SRC)

test:
	go test ./...

smoke:
	@# 真机冒烟：默认参数跑一次真实下载并校验产物（需联网，详见 scripts/smoke.sh）
	bash scripts/smoke.sh

clean:
	rm -rf bin/
