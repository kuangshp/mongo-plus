package mongoplus_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type aggregateItem struct {
	Price int64 `bson:"price"`
}
type aggregateDocument struct {
	ID        int64           `bson:"_id"`
	Category  string          `bson:"category"`
	Status    int64           `bson:"status"`
	Quantity  int64           `bson:"quantity"`
	Amount    bson.Decimal128 `bson:"amount"`
	Score     *float64        `bson:"score"`
	Tags      []string        `bson:"tags"`
	Items     []aggregateItem `bson:"items"`
	CompanyID int64           `bson:"company_id"`
	DeletedAt *time.Time      `bson:"deleted_at"`
}

func aggregateFixture(t *testing.T) (*mp.Database, *mp.Repository[aggregateDocument, int64], context.Context) {
	t.Helper()
	db := mp.NewDatabase(testutil.Database(t))
	r := mp.MustRepository[aggregateDocument, int64](db, mp.CollectionConfig{Name: "orders", ID: mp.IDConfig{Type: "int64", Generation: "manual"}, SoftDelete: true, TenantField: "company_id"})
	ctx := mp.WithTenant(context.Background(), int64(1))
	score := func(n float64) *float64 { return &n }
	docs := []aggregateDocument{
		{ID: 1, Category: "A", Status: 1, Quantity: 2, Score: score(10), Tags: []string{"red", "red", "blue"}, Items: []aggregateItem{{Price: 2}, {Price: 3}}},
		{ID: 2, Category: "A", Status: 1, Quantity: 3, Score: score(20), Tags: []string{"red"}, Items: []aggregateItem{{Price: 4}}},
		{ID: 3, Category: "A", Status: 2, Quantity: 5},
		{ID: 4, Category: "B", Status: 1, Quantity: 7, Score: score(30), Tags: []string{}},
		{ID: 5, Category: "B", Status: 2, Quantity: 11},
		{ID: 6, Category: "other tenant", Quantity: 100},
		{ID: 7, Category: "deleted", Quantity: 1000},
	}
	for i, text := range []string{"10.50", "20.25", "4.25", "15.00", "0.00", "999.00", "9999.00"} {
		var err error
		docs[i].Amount, err = bson.ParseDecimal128(text)
		if err != nil {
			t.Fatal(err)
		}
		createCtx := ctx
		if docs[i].ID == 6 {
			createCtx = mp.WithTenant(ctx, int64(2))
		}
		if err := r.Create(createCtx, &docs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.DeleteById(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateById(ctx, 5, field.New[*float64]("score").Unset()); err != nil {
		t.Fatal(err)
	}
	return db, r, ctx
}

func TestAggregateStatisticsAndDecimalPrecision(t *testing.T) {
	_, r, ctx := aggregateFixture(t)
	type summary struct {
		Count         int64           `bson:"count"`
		Total         int64           `bson:"total"`
		Avg           float64         `bson:"avg"`
		Min           int64           `bson:"min"`
		Max           int64           `bson:"max"`
		Score         float64         `bson:"score"`
		Population    float64         `bson:"population"`
		Sample        float64         `bson:"sample"`
		Amount        bson.Decimal128 `bson:"amount"`
		AverageAmount bson.Decimal128 `bson:"averageAmount"`
		First         int64           `bson:"first"`
		Last          int64           `bson:"last"`
		IDs           []int64         `bson:"ids"`
		Categories    []string        `bson:"categories"`
	}
	var got summary
	err := r.WithContext(ctx).Sort(field.New[int64]("_id").Asc()).
		Count("count").Sum(field.New[int64]("quantity"), "total").Avg(field.New[int64]("quantity"), "avg").
		Min(field.New[int64]("quantity"), "min").Max(field.New[int64]("quantity"), "max").
		Avg(field.New[*float64]("score"), "score").StdDevPop(field.New[*float64]("score"), "population").StdDevSamp(field.New[*float64]("score"), "sample").
		Sum(field.New[bson.Decimal128]("amount"), "amount").Avg(field.New[bson.Decimal128]("amount"), "averageAmount").
		First(field.New[int64]("_id"), "first").Last(field.New[int64]("_id"), "last").
		Push(field.New[int64]("_id"), "ids").AddToSet(field.NewString("category"), "categories").ScanOne(&got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 5 || got.Total != 28 || got.Avg != 5.6 || got.Min != 2 || got.Max != 11 || got.Score != 20 || got.First != 1 || got.Last != 5 {
		t.Fatalf("统计错误 %+v", got)
	}
	if math.Abs(got.Population-math.Sqrt(200.0/3)) > 1e-9 || got.Sample != 10 {
		t.Fatalf("标准差错误 %+v", got)
	}
	if got.Amount.String() != "50.00" || got.AverageAmount.String() != "10.00" {
		t.Fatalf("Decimal128 精度丢失 %s %s", got.Amount.String(), got.AverageAmount.String())
	}
	if !reflect.DeepEqual(got.IDs, []int64{1, 2, 3, 4, 5}) || len(got.Categories) != 2 {
		t.Fatalf("收集结果错误 %+v", got)
	}
	var deleted struct {
		Count int64 `bson:"count"`
	}
	if err := r.WithContext(ctx).OnlyDeleted().Count("count").ScanOne(&deleted); err != nil || deleted.Count != 1 {
		t.Fatalf("逻辑删除统计错误 %+v %v", deleted, err)
	}
}

func TestAggregateGroupHavingPaginationAndMultipleStages(t *testing.T) {
	_, r, ctx := aggregateFixture(t)
	type stats struct {
		Category string `bson:"category"`
		State    int64  `bson:"state"`
		Count    int64  `bson:"count"`
		Total    int64  `bson:"total"`
	}
	var rows []stats
	q := r.WithContext(ctx).GroupBy(field.NewString("category")).Count("count").Sum(field.New[int64]("quantity"), "total").Order(field.NewString("category").Asc())
	total, err := q.ScanByPage(&rows, 1, 1)
	if err != nil || total != 2 || len(rows) != 1 || rows[0].Category != "A" || rows[0].Count != 3 || rows[0].Total != 10 {
		t.Fatalf("分组分页错误 %+v %d %v", rows, total, err)
	}
	total, err = q.ScanByPage(&rows, 2, 1)
	if err != nil || total != 2 || len(rows) != 1 || rows[0].Category != "B" || rows[0].Total != 18 {
		t.Fatalf("分组第二页错误 %+v %d %v", rows, total, err)
	}
	total, err = q.ScanByPage(&rows, 99, 1)
	if err != nil || total != 2 || rows == nil || len(rows) != 0 {
		t.Fatalf("超出末页错误 %+v %d %v", rows, total, err)
	}
	err = q.WithContext(ctx).Having(field.NewNumeric[int64]("count").Gt(2)).Scan(&rows)
	if err != nil || len(rows) != 1 || rows[0].Category != "A" {
		t.Fatalf("Having 错误 %+v %v", rows, err)
	}
	err = r.WithContext(ctx).Where(field.New[int64]("status").Eq(1)).GroupBy(field.NewString("category"), field.New[int64]("status").As("state")).Count("count").Order(field.NewString("category").Asc()).Scan(&rows)
	if err != nil || len(rows) != 2 || rows[0].State != 1 || rows[0].Count != 2 || rows[1].Count != 1 {
		t.Fatalf("多字段分组与预筛选错误 %+v %v", rows, err)
	}
	var second struct {
		Total  int64 `bson:"grand"`
		Groups int64 `bson:"groups"`
	}
	err = r.WithContext(ctx).GroupBy(field.NewString("category")).Sum(field.New[int64]("quantity"), "subtotal").GroupBy().Sum(field.New[int64]("subtotal"), "grand").Count("groups").ScanOne(&second)
	if err != nil || second.Total != 28 || second.Groups != 2 {
		t.Fatalf("二次分组错误 %+v %v", second, err)
	}
	empty := r.WithContext(ctx).Where(field.New[int64]("_id").Eq(999)).Count("count")
	if err := empty.ScanOne(&stats{}); !errors.Is(err, mp.ErrNotFound) {
		t.Fatalf("空统计应保留原生无记录语义 %v", err)
	}
	total, err = empty.ScanByPage(&rows, 1, 10)
	if err != nil || total != 0 || rows == nil || len(rows) != 0 {
		t.Fatalf("空统计分页错误 %+v %d %v", rows, total, err)
	}
	var scores []struct {
		Score *float64 `bson:"score"`
		Count int64    `bson:"count"`
	}
	err = r.WithContext(ctx).GroupBy(field.New[*float64]("score")).Count("count").Order(field.New[*float64]("score").Asc()).Scan(&scores)
	if err != nil || len(scores) != 4 || scores[0].Score != nil || scores[0].Count != 2 {
		t.Fatalf("缺失与 null 应归为同组: %+v %v", scores, err)
	}
}

func TestAggregateUnwindAndLookup(t *testing.T) {
	_, r, ctx := aggregateFixture(t)
	type tagStats struct {
		Tag   string `bson:"tag"`
		Count int64  `bson:"count"`
	}
	var tags []tagStats
	err := r.WithContext(ctx).Unwind(field.NewArray[string]("tags")).GroupBy(field.NewString("tags").As("tag")).Count("count").Order(field.NewString("tag").Asc()).Scan(&tags)
	if err != nil || !reflect.DeepEqual(tags, []tagStats{{Tag: "blue", Count: 1}, {Tag: "red", Count: 3}}) {
		t.Fatalf("数组元素统计错误 %+v %v", tags, err)
	}
	var values []bson.M
	unwound := r.WithContext(ctx).Unwind(field.NewArray[string]("tags"))
	total, err := unwound.ScanByPage(&values, 2, 2)
	if err != nil || total != 4 || len(values) != 2 {
		t.Fatalf("数组展开分页错误 %+v %d %v", values, total, err)
	}
	for _, value := range values {
		for key := range value {
			if len(key) >= 5 && key[:5] == "__mp_" {
				t.Fatalf("内部索引泄露 %+v", value)
			}
		}
	}
	var withEmpty []bson.M
	if err := r.WithContext(ctx).Unwind(field.NewArray[string]("tags"), true).Scan(&withEmpty); err != nil || len(withEmpty) != 7 {
		t.Fatalf("保留空数组错误 %d %v", len(withEmpty), err)
	}
	var nested struct {
		Total int64 `bson:"total"`
	}
	if err := r.WithContext(ctx).Unwind(field.NewArray[aggregateItem]("items")).Sum(field.New[int64]("items.price"), "total").ScanOne(&nested); err != nil || nested.Total != 9 {
		t.Fatalf("嵌套数组求和错误 %+v %v", nested, err)
	}
	f := joinedFixture(t)
	var departmentRows []struct {
		Department string `bson:"departmentName"`
		Count      int64  `bson:"count"`
	}
	err = f.users.WithContext(f.ctx).InnerJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department").
		GroupBy(field.NewString("name").At("department").As("departmentName")).Count("count").Scan(&departmentRows)
	if err != nil || len(departmentRows) != 1 || departmentRows[0].Department != "研发" || departmentRows[0].Count != 1 {
		t.Fatalf("关联分组隔离失败 %+v %v", departmentRows, err)
	}
	var roleRows []struct {
		Name  string `bson:"name"`
		Count int64  `bson:"count"`
	}
	err = f.users.WithContext(f.ctx).Lookup(f.roles, field.New[int64]("_id"), field.New[int64]("user_id"), "roles").Unwind(field.NewArray[joinRole]("roles")).GroupBy(field.NewString("name").At("roles")).Count("count").Scan(&roleRows)
	if err != nil || len(roleRows) != 2 {
		t.Fatalf("Lookup 数组展开后分组失败 %+v %v", roleRows, err)
	}
}

func TestAggregateInTransactionAndProjectionAliases(t *testing.T) {
	db, r, ctx := aggregateFixture(t)
	abort := errors.New("rollback statistics")
	var sum struct {
		Total int64 `bson:"total"`
	}
	err := mp.Transaction(ctx, db, func(txCtx context.Context) error {
		if _, err := r.UpdateById(txCtx, 1, field.NewNumeric[int64]("quantity").Inc(1)); err != nil {
			return err
		}
		if err := r.WithContext(txCtx).Sum(field.New[int64]("quantity"), "total").ScanOne(&sum); err != nil {
			return err
		}
		if sum.Total != 29 {
			return errors.New("统计未读取事务数据")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if err := r.WithContext(ctx).Sum(field.New[int64]("quantity"), "total").ScanOne(&sum); err != nil || sum.Total != 28 {
		t.Fatalf("事务回滚失败 %+v %v", sum, err)
	}
	var projected []struct {
		Name string `bson:"name"`
	}
	err = r.WithContext(ctx).Where(field.New[int64]("_id").Eq(1)).Select(field.NewString("category").As("name")).Scan(&projected)
	if err != nil || len(projected) != 1 || projected[0].Name != "A" {
		t.Fatalf("别名投影失败 %+v %v", projected, err)
	}
}
