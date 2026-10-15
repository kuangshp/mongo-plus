package mongoplus_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type benchDocument struct {
	ID      bson.ObjectID `bson:"_id"`
	Payload string        `bson:"payload"`
}
type benchNumericDocument struct {
	ID      int64  `bson:"_id"`
	Payload string `bson:"payload"`
}

// BenchmarkRepository 使用测试专用数据库；未设置 MONGOPLUS_TEST_URI 时跳过。
func BenchmarkRepository(b *testing.B) {
	ctx := context.Background()
	db := mp.NewDatabase(testutil.Database(b))
	payload := strings.Repeat("x", 256)
	r := mp.MustRepository[benchDocument, bson.ObjectID](db, mp.CollectionConfig{Name: "objects"})
	seed := &benchDocument{Payload: payload}
	if err := r.Create(ctx, seed); err != nil {
		b.Fatal(err)
	}
	b.Run("FindById", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := r.FindById(ctx, seed.ID); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, mode := range []string{"NativeInsertMany", "CreateBatch"} {
		b.Run(mode+"100", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				rows := make([]*benchDocument, 100)
				for j := range rows {
					rows[j] = &benchDocument{ID: bson.NewObjectID(), Payload: payload}
				}
				var err error
				if mode == "NativeInsertMany" {
					_, err = r.Native().InsertMany(ctx, rows)
				} else {
					err = r.CreateBatch(ctx, rows)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	for _, blockSize := range []int64{0, 100} {
		r := mp.MustRepository[benchNumericDocument, int64](db, mp.CollectionConfig{Name: fmt.Sprintf("numbers_%d", blockSize), ID: mp.IDConfig{Type: mp.IDTypeInt64, BlockSize: blockSize}})
		b.Run(fmt.Sprintf("CreateInt64Block%d", blockSize), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := r.Create(ctx, &benchNumericDocument{Payload: payload}); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("CreateBatchInt64Block%d", blockSize), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				rows := make([]*benchNumericDocument, 100)
				for j := range rows {
					rows[j] = &benchNumericDocument{Payload: payload}
				}
				if err := r.CreateBatch(ctx, rows); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
