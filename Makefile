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

loadtest:
	go run ./cmd/mongoplus-loadtest $(LOADTEST_ARGS)

# 只校验，不提交、不创建标签、不推送；要求显式指定测试专用 MongoDB。
release-check:
	bash scripts/release.sh check

# 版本和发布记录必须提前准备、全部代码提前提交；校验通过后发布当前提交。
# 不再只提交 version.go，不自动递增版本，也不跳过集成/安全检查。
tag:
	bash scripts/release.sh publish
