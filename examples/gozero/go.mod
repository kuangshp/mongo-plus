module github.com/kuangshp/mongo-plus/examples/gozero

go 1.25.5

require (
	github.com/kuangshp/mongo-plus v0.0.0
	github.com/zeromicro/go-zero v1.10.2
)

// 本地示例使用仓库源码；业务项目发布后使用实际版本，不复制此 replace。
replace github.com/kuangshp/mongo-plus => ../..
