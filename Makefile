SHELL := /bin/bash

.PHONY: test verify security bench loadtest release-check tag

test:
	go test ./...

verify:
	go vet ./...
	go test -race -count=1 ./...

security:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

bench:
	go test -run '^$$' -bench '^BenchmarkRepository$$' -benchmem .

# 显式传测试专用 URI，例如 make loadtest LOADTEST_ARGS='-uri ... -duration 30s'。
loadtest:
	go run ./cmd/mongoplus-loadtest $(LOADTEST_ARGS)

# 不创建提交、标签或推送。
release-check:
	bash scripts/release.sh check

# 只发布已提交的完整修复，不再自动递增并单独提交版本号。
tag:
	bash scripts/release.sh publish
