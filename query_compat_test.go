package mongoplus

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBetweenMatchesGormPlus(t *testing.T) {
	var nilTime *time.Time
	for _, pair := range [][2]any{{nil, 2}, {1, nil}, {0, 2}, {1, 0}, {"", "z"}, {time.Time{}, time.Now()}, {nilTime, time.Now()}} {
		w := Query().BetweenIfNotZero(field.New[any]("range"), pair[0], pair[1])
		if len(w.Build().conditions) != 0 {
			t.Fatalf("单端零值没有跳过整个范围: %v", pair)
		}
	}
	w := Query().BetweenIfNotZero(field.New[int64]("age"), int64(1), int64(9))
	if err := w.Build().validate(); err != nil {
		t.Fatal(err)
	}
	want := field.And(field.New[any]("age").Gte(int64(1)), field.New[any]("age").Lte(int64(9)))
	if got := field.And(w.Build().conditions...); !reflect.DeepEqual(got, want) {
		t.Fatalf("范围应包含两个边界: %+v", got)
	}
	if len(Query().BetweenIfNotZero(nil, 1, 2).Build().conditions) != 0 {
		t.Fatal("空字段应跳过")
	}
}

func TestWrapperGroupsAndTenantIsolation(t *testing.T) {
	a, b, c := field.NewString("name").Eq("a"), field.NewString("name").Eq("b"), field.NewString("name").Eq("c")
	for name, tc := range map[string]struct {
		query *Wrapper
		want  Condition
	}{
		"or each":      {Query().Where(a).OrWhereIf(true, b, c), field.Or(field.Or(a, b), c)},
		"or and group": {Query().Where(a).OrGroup(b, c), field.Or(a, field.And(b, c))},
		"and callback": {Query().Where(a).WhereGroupFn(func(w *Wrapper) { w.Where(b).OrGroup(c) }), field.And(a, field.Or(b, c))},
		"or callback":  {Query().Where(a).OrGroupFn(func(w *Wrapper) { w.Where(b, c) }), field.Or(a, field.And(b, c))},
	} {
		t.Run(name, func(t *testing.T) {
			if got := field.And(tc.query.Build().conditions...); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("分组不一致: %+v != %+v", got, tc.want)
			}
		})
	}
	root, _, _ := querySources(t)
	root.config.TenantField = "company_id"
	o := Query().Where(a).OrGroup(b, c).WithUnscoped().Build()
	filter, err := root.filter(WithTenant(context.Background(), int64(7)), o, false)
	want := field.And(field.Or(a, field.And(b, c)), field.New[any]("company_id").Eq(int64(7)))
	if err != nil || !reflect.DeepEqual(filter, want.Document) {
		t.Fatalf("OR/Unscoped 影响租户条件: %v %v", filter, err)
	}
	if _, err := root.filter(context.Background(), o, false); !errors.Is(err, ErrTenantRequired) {
		t.Fatal(err)
	}
	for _, w := range []*Wrapper{
		Query().WhereGroupFn(nil), Query().OrGroupFn(func(w *Wrapper) { w.Like(field.NewString("name"), "").AllowAll() }),
		Query().OrWhereIf(false, a), Query().OrGroupIf(false, a), Query().OrWhereRaw(nil),
	} {
		if _, err := root.filter(context.Background(), w.Build(), true); !errors.Is(err, ErrUnsafeWrite) {
			t.Fatalf("空分组放开批量写入: %v", err)
		}
	}
	if err := Query().WhereGroupFn(func(w *Wrapper) { w.Where(Condition{Err: errors.New("bad")}) }).Build().validate(); err == nil {
		t.Fatal("子分组错误丢失")
	}
}

func TestCompatibilityLikeAndRawConditions(t *testing.T) {
	f := field.NewString("name")
	for name, tc := range map[string]struct {
		query *Wrapper
		want  Condition
	}{
		"suffix":      {Query().LLikeIf(true, f, ".*"), f.EndsWith(".*")},
		"prefix":      {Query().RLikeIf(true, f, ".*"), f.StartsWith(".*")},
		"or contains": {Query().Where(f.Eq("a")).OrLikeIf(true, f, ".*"), field.Or(f.Eq("a"), f.Contains(".*"))},
		"or suffix":   {Query().OrLLikeIf(true, f, ".*"), f.EndsWith(".*")},
		"or prefix":   {Query().OrRLikeIf(true, f, ".*"), f.StartsWith(".*")},
		"raw":         {Query().WhereRawIf(true, f.Eq("a").Document).OrWhereRaw(f.Eq("b").Document), field.Or(f.Eq("a"), f.Eq("b"))},
	} {
		t.Run(name, func(t *testing.T) {
			if got := field.And(tc.query.Build().conditions...); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("字面匹配/原生分组不一致: %v", got)
			}
		})
	}
	w := Query().OrLike(f, "").LLikeIf(false, f, "x").RLikeIf(false, f, "x").OrLikeIf(false, f, "x").OrLLikeIf(false, f, "x").OrRLikeIf(false, f, "x")
	if len(w.Build().conditions) != 0 {
		t.Fatal("可选匹配没有跳过")
	}
}

func TestCompatibleSortingAndSnapshots(t *testing.T) {
	f := field.New[int64]("age")
	for _, state := range []any{int8(1), int64(1), uint64(1), float64(1), "1", "asc", "ASC"} {
		if got := QueryOpt().OrderTriState(state, f.Asc(), f.Desc()).Build().sort; !reflect.DeepEqual(got, bson.D{f.Asc()}) {
			t.Fatal(state, got)
		}
	}
	for _, state := range []any{2, uint32(2), "2", "desc", "DESC"} {
		if got := QueryOpt().OrderTriState(state, f.Asc(), f.Desc()).Build().sort; !reflect.DeepEqual(got, bson.D{f.Desc()}) {
			t.Fatal(state, got)
		}
	}
	for _, state := range []any{nil, 0, -1, "invalid", false, uint64(1 << 63)} {
		if got := QueryOpt().OrderTriState(state, f.Asc(), f.Desc()).Build().sort; len(got) != 0 {
			t.Fatal(state, got)
		}
	}
	w := QueryOpt().OrderDefault(f.Desc())
	before := w.Build()
	w.OrderIf(true, f.Asc()).OrderDefault(field.New[int64]("_id").Desc())
	if !reflect.DeepEqual(before.sort, bson.D{f.Desc()}) || !reflect.DeepEqual(w.Build().sort, bson.D{f.Asc()}) {
		t.Fatal("默认排序影响显式排序或已有快照")
	}
	if got := Query().OrderIf(false, f.Asc(), f.Desc()).Build().sort; !reflect.DeepEqual(got, bson.D{f.Desc()}) {
		t.Fatal(got)
	}
}

func TestSingleFlightTTLCompatibility(t *testing.T) {
	for _, w := range []*Wrapper{Query().WithSingleFlight(), QueryOpt().WithSingleFlight(0)} {
		if o := w.Build(); !o.singleFlight || o.cacheTTL != 0 || o.validate() != nil {
			t.Fatal(o)
		}
	}
	if o := QueryOpt().WithSingleFlight(time.Minute).Build(); !o.singleFlight || o.cacheTTL != time.Minute {
		t.Fatal(o)
	}
	for _, w := range []*Wrapper{
		Query().WithCache(2 * time.Minute).WithSingleFlight(0),
		Query().WithCache(2 * time.Minute).WithSingleFlight(time.Minute),
		Query().WithSingleFlight(time.Minute).WithCache(2 * time.Minute),
	} {
		if o := w.Build(); o.cacheTTL != 2*time.Minute {
			t.Fatalf("SF 覆盖显式缓存 TTL: %v", o.cacheTTL)
		}
	}
	for _, w := range []*Wrapper{Query().WithSingleFlight(-1), Query().WithSingleFlight(0, 0)} {
		if w.Build().validate() == nil {
			t.Fatal("无效 TTL 未报错")
		}
	}
}

func TestEmptyWrapperAndBulkDeleteSafety(t *testing.T) {
	root, _, _ := querySources(t)
	ctx := context.Background()
	for _, ids := range [][]int64{nil, {}} {
		if n, err := root.DeleteByIdList(ctx, ids); n != 0 || err != nil {
			t.Fatal(n, err)
		}
	}
	for _, ids := range [][]int64{{0}, {1, -1}} {
		if _, err := root.DeleteByIdList(ctx, ids, WithPhysicalDelete()); !errors.Is(err, ErrInvalidID) {
			t.Fatal(err)
		}
	}
	for _, fn := range []func() error{
		func() error { _, err := root.DeleteByCondition(ctx); return err },
		func() error { _, err := root.DeleteByWrapper(ctx, nil); return err },
		func() error {
			_, err := root.DeleteByWrapper(ctx, nil, Delete().WithPhysicalDelete().Build())
			return err
		},
		func() error { _, err := root.UpdateByWrapper(ctx, nil, field.NewString("name").Set("x")); return err },
	} {
		if err := fn(); !errors.Is(err, ErrUnsafeWrite) {
			t.Fatal(err)
		}
	}
	bad := Query().Limit(-1).Build()
	if _, err := root.CountByWrapper(ctx, nil, bad); err == nil {
		t.Fatal("CountByWrapper 没有传递选项")
	}
	if _, err := root.ExistsByWrapper(ctx, nil, bad); err == nil {
		t.Fatal("ExistsByWrapper 没有传递选项")
	}
}
