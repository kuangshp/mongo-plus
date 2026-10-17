module github.com/kuangshp/mongo-plus/examples/gozero

go 1.25.5

toolchain go1.26.9

require (
	github.com/kuangshp/mongo-plus v0.0.0
	github.com/zeromicro/go-zero v1.10.2
	gopkg.in/yaml.v3 v3.0.1
)

// 本地示例使用仓库源码；业务项目发布后使用实际版本，不复制此 replace。
replace github.com/kuangshp/mongo-plus => ../..
