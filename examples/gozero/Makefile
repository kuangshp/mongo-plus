.PHONY: gen install-tools validate swagger run run-open build schema-plan schema-apply

# 示例从当前本地 mongo-plus 源码安装 CLI；不会安装尚未发布的 latest。
install-tools:
	go install github.com/zeromicro/go-zero/tools/goctl@v1.10.2
	go install -mod=mod github.com/kuangshp/mongo-plus/cmd/mongoplus

validate: install-tools
	goctl api validate --api shop.api

# 读取 YAML；配置 mongodb 时先同步数据库，再生成数据层和 HTTP 层。
gen: validate
	go run generator.go
	mongoplus goctl api go --api shop.api --dir . --style=goZero

swagger:
	mongoplus swagger --config etc/shop-api.yaml

run: swagger
	go run . -f etc/shop-api.yaml

run-open: swagger
	@mongoplus swagger-open --config etc/shop-api.yaml &
	go run . -f etc/shop-api.yaml

build: swagger
	mkdir -p bin
	go build -o bin/shop .

# 配置 mongodb 后可单独预览或同步；启动服务不自动同步结构。
schema-plan:
	mongoplus schema plan --config mongo-plus.yaml

schema-apply:
	mongoplus schema apply --config mongo-plus.yaml
