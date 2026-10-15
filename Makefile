.PHONY: gen check test integration vet build schema-plan schema-apply example statistics-example

# 所有命令默认在 mongo-plus 项目根目录运行，不需要先安装工具。
gen:
	go run generator.go

check:
	go run ./cmd/mongoplus check --config examples/mongo-plus.yaml

test:
	go test ./...

# 需要显式设置测试专用副本集 URI，测试只创建和清理随机命名数据库。
integration:
	@test -n "$$MONGOPLUS_TEST_URI" || (echo '请设置 MONGOPLUS_TEST_URI'; exit 1)
	go test -race -count=1 ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	go build -o bin/mongoplus ./cmd/mongoplus

schema-plan:
	go run ./cmd/mongoplus schema plan --config examples/mongo-plus.yaml

schema-apply:
	go run ./cmd/mongoplus schema apply --config examples/mongo-plus.yaml

example:
	go run ./examples

# 订单分组、Decimal128 金额统计、数组展开；先执行 schema-apply。
statistics-example:
	go run ./examples/statistics
