// statistics 演示链式聚合；先在项目根目录执行 make gen 和 make schema-apply。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/examples/query/dao"
	"github.com/kuangshp/mongo-plus/examples/query/model"
	"github.com/kuangshp/mongo-plus/examples/query/repository"
	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type CategoryStats struct {
	Category string          `bson:"category"`
	Count    int64           `bson:"count"`
	Total    bson.Decimal128 `bson:"total"`
	Average  bson.Decimal128 `bson:"average"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := mp.Connect(ctx, mp.Config{URI: os.Getenv("MONGO_URI"), Database: "mongo_plus_demo"})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = db.Close(closeCtx)
	}()
	orders := repository.NewOrderRepository(db)
	var ids []int64
	for i, text := range []string{"10.50", "20.25", "4.25"} {
		amount, err := bson.ParseDecimal128(text)
		if err != nil {
			return err
		}
		category := "图书"
		if i == 2 {
			category = "文具"
		}
		order := &model.OrderEntity{Category: category, Status: 2, Quantity: int64(i + 1), Amount: amount, Tags: []string{"演示", category}}
		if err := orders.Create(ctx, order); err != nil {
			return err
		}
		ids = append(ids, order.ID)
	}
	// 只统计本次创建的订单，重复运行不会让示例结果累加。
	query := orders.WithContext(ctx).Where(dao.OrderEntity.ID.In(ids...))
	var summary struct {
		Count    int64           `bson:"count"`
		Total    bson.Decimal128 `bson:"total"`
		Quantity int64           `bson:"quantity"`
	}
	if err := query.WithContext(ctx).Count("count").Sum(dao.OrderEntity.Amount, "total").Sum(dao.OrderEntity.Quantity, "quantity").ScanOne(&summary); err != nil {
		return err
	}
	fmt.Printf("全部订单：%d 笔，数量 %d，金额 %s\n", summary.Count, summary.Quantity, summary.Total.String())
	var rows []CategoryStats
	grouped := query.WithContext(ctx).
		GroupBy(dao.OrderEntity.Category).
		Count("count").
		Sum(dao.OrderEntity.Amount, "total").
		Avg(dao.OrderEntity.Amount, "average").
		Having(field.NewNumeric[int64]("count").Gte(1)).
		Order(dao.OrderEntity.Category.Asc())
	total, err := grouped.ScanByPage(&rows, 1, 10)
	if err != nil {
		return err
	}
	fmt.Printf("分类统计：共 %d 组\n", total)
	for _, row := range rows {
		fmt.Printf("  %s：%d 笔，总金额 %s，平均金额 %s\n", row.Category, row.Count, row.Total.String(), row.Average.String())
	}
	var tagRows []struct {
		Tag   string `bson:"tag"`
		Count int64  `bson:"count"`
	}
	if err := query.WithContext(ctx).Unwind(dao.OrderEntity.Tags).GroupBy(dao.OrderEntity.Tags.As("tag")).Count("count").Order(field.NewString("tag").Asc()).Scan(&tagRows); err != nil {
		return err
	}
	for _, row := range tagRows {
		fmt.Printf("标签 %s：%d 次\n", row.Tag, row.Count)
	}
	return nil
}
