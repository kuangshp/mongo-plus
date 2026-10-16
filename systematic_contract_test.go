package mongoplus

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSharedCacheValueFidelityMatrix(t *testing.T) {
	type containers struct {
		Items  []string         `bson:"items,omitempty"`
		Lookup map[string]int64 `bson:"lookup,omitempty"`
	}
	type child struct {
		Name string `bson:"name,omitempty"`
	}
	type inline struct {
		*child `bson:",inline"`
	}
	tests := []struct {
		name   string
		value  any
		stable bool
	}{
		{"empty_slice", containers{Items: []string{}}, false},
		{"empty_map", containers{Lookup: map[string]int64{}}, false},
		{"nil_containers", containers{}, true},
		{"populated", containers{Items: []string{"a"}, Lookup: map[string]int64{"a": 1}}, true},
		{"inline_empty", inline{&child{}}, false},
		{"nan", math.NaN(), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// 使用实际静态类型执行编解码，不能将测试值擦除为 interface 后误判类型。
			var stable bool
			switch value := test.value.(type) {
			case containers:
				_, stable = encodeSharedValue(value)
			case inline:
				_, stable = encodeSharedValue(value)
			case float64:
				_, stable = encodeSharedValue(value)
			}
			if stable != test.stable {
				t.Fatalf("共享缓存保真判断: %t want %t", stable, test.stable)
			}
		})
	}
	// 可缓存结果必须真正命中，且返回值不与上个调用共享可变切片。
	db := &Database{}
	store := NewMemoryCache()
	t.Cleanup(func() { _ = store.Close() })
	db.UseCache(store)
	loads := 0
	load := func() (containers, error) { loads++; return containers{Items: []string{"a"}}, nil }
	option := Query().WithCache(time.Minute).Build()
	first, err := readCached(context.Background(), db, "faithful", "one", bson.D{}, option, load)
	if err != nil {
		t.Fatal(err)
	}
	first.Items[0] = "mutated"
	next, err := readCached(context.Background(), db, "faithful", "one", bson.D{}, option, load)
	if err != nil || loads != 1 || !reflect.DeepEqual(next.Items, []string{"a"}) {
		t.Fatalf("合法缓存未命中或副本污染: %+v loads=%d %v", next, loads, err)
	}
}

func TestExpressionLogsNeverExposeConstants(t *testing.T) {
	secret := "13800008000"
	cases := []bson.D{
		{{Key: "filter", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$phone", secret}}}}}}},
		{{Key: "let", Value: bson.D{{Key: "p", Value: secret}}}},
		{{Key: "pipeline", Value: bson.A{bson.D{{Key: "$project", Value: bson.D{{Key: "alias", Value: bson.D{{Key: "$cond", Value: bson.A{true, secret, "other"}}}}}}}}}},
		{{Key: "filter", Value: bson.D{{Key: "$where", Value: "return this.phone == '" + secret + "'"}}}},
		{{Key: "pipeline", Value: bson.A{bson.D{{Key: "$set", Value: bson.D{{Key: "alias", Value: bson.D{{Key: "$literal", Value: secret}}}}}}}}},
	}
	for i, doc := range cases {
		doc = append(bson.D{{Key: "aggregate", Value: "users"}}, doc...)
		got := safeCommand(logRaw(t, doc), LogConfig{MaxLength: 4096})
		if strings.Contains(got, secret) || !strings.Contains(got, "users") {
			t.Fatalf("case %d 表达式泄露: %s", i, got)
		}
	}
}

func TestPublicEntryPointsRejectInvalidInputs(t *testing.T) {
	if _, err := Aggregate[bson.M, scopeReviewRow, int64](context.Background(), nil, nil); err == nil {
		t.Fatal("nil 聚合仓储应返回错误")
	}
	if _, err := DALQuery[bson.M](nil, "rows.json"); err == nil {
		t.Fatal("nil DAL context 应返回错误")
	}
	r := &Repository[scopeReviewRow, int64]{}
	if err := r.Walk(context.Background(), nil); err == nil {
		t.Fatal("nil Walk 回调应返回错误")
	}
}
