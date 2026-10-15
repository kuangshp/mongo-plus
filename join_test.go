package mongoplus

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type queryEntity struct {
	ID        int64  `bson:"_id"`
	OwnerID   int64  `bson:"owner_id"`
	WrongID   string `bson:"wrong_id"`
	Name      string `bson:"name"`
	CompanyID int64  `bson:"company_id"`
}

// querySources 只创建驱动句柄，不连接/读取业务数据库。
func querySources(t *testing.T) (*Repository[queryEntity, int64], *Repository[queryEntity, int64], *Repository[queryEntity, int64]) {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1").SetServerSelectionTimeout(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	cfg := CollectionConfig{Name: "main", ID: IDConfig{Type: "int64", Generation: "manual"}, SoftDelete: true}
	root := MustRepository[queryEntity, int64](NewDatabase(client.Database("offline")), cfg)
	cfg.Name = "foreign"
	foreign := MustRepository[queryEntity, int64](root.db, cfg)
	cross := MustRepository[queryEntity, int64](NewDatabase(client.Database("other")), cfg)
	return root, foreign, cross
}

func TestJoinRejectsInvalidPlansBeforeExecution(t *testing.T) {
	root, foreign, cross := querySources(t)
	id := field.New[int64]("_id")
	owner := field.New[int64]("owner_id")
	for name, query := range map[string]*Aggregation{
		"nil source":             From(nil),
		"zero builder":           {},
		"source field collision": From(root).LeftJoin(foreign, owner, id, "name"),
		"reserved collision":     From(root).LeftJoin(foreign, owner, id, "deleted_at"),
		"dotted alias":           From(root).LeftJoin(foreign, owner, id, "x.y"),
		"duplicate alias":        From(root).LeftJoin(foreign, owner, id, "owner").Lookup(foreign, owner, id, "owner"),
		"unknown local":          From(root).LeftJoin(foreign, field.New[int64]("missing"), id, "owner"),
		"unknown foreign":        From(root).LeftJoin(foreign, owner, field.New[int64]("missing"), "owner"),
		"unknown prior alias":    From(root).LeftJoin(foreign, owner.At("later"), id, "owner"),
		"type mismatch":          From(root).LeftJoin(foreign, field.NewString("wrong_id"), id, "owner"),
		"different database":     From(root).LeftJoin(cross, owner, id, "owner"),
		"nil field":              From(root).LeftJoin(foreign, nil, id, "owner"),
		"main cache":             From(root, Query().WithCache(time.Minute).Build()),
		"foreign cache":          From(root).Lookup(foreign, owner, id, "owner", Query().WithSingleFlight().Build()),
		"mixed projections":      From(root).Select(id).Project(bson.D{{Key: "name", Value: 1}}),
		"invalid where":          From(root).WhereOrGroup(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := query.Build(context.Background()); err == nil {
				t.Fatal("无效关联计划未被拒绝")
			}
		})
	}
	var nilQuery *Aggregation
	if _, err := Scan[bson.M](context.Background(), nilQuery); err == nil {
		t.Fatal("nil 查询未返回错误")
	}
}

func TestJoinScopesAndPaginationValidation(t *testing.T) {
	root, foreign, _ := querySources(t)
	ctx := context.Background()
	id := field.New[int64]("_id")
	owner := field.New[int64]("owner_id")
	foreign.config.TenantField = "company_id"
	if _, err := From(root).Lookup(foreign, owner, id, "owner").Build(ctx); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("关联集合未检查租户: %v", err)
	}
	ctx = WithTenant(ctx, int64(9))
	query := From(root).LeftJoin(foreign, owner, id, "owner", Query().Where(field.New[int64]("company_id").Eq(88)).Build())
	pipeline, err := query.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	json, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"company_id", "deleted_at", "$and", "mp_local"} {
		if !strings.Contains(string(json), fragment) {
			t.Fatalf("缺少隔离条件 %s: %s", fragment, json)
		}
	}
	if _, _, err := From(root).Limit(5).buildBase(ctx, true); err == nil {
		t.Fatal("分页与 Limit 混用未报错")
	}
	if _, _, err := From(root).LeftJoin(foreign, owner, id, "owner", Query().Omit(id).Build()).buildBase(ctx, true); err == nil {
		t.Fatal("展开分页未保留排序主键")
	}
	if _, _, err := From(root).Lookup(foreign, owner, id, "owners", Query().Omit(id).Build()).buildBase(ctx, true); err != nil {
		t.Fatalf("数组关联投影不应影响主记录排序 %v", err)
	}
}

func TestAliasedFieldsKeepTheirOperations(t *testing.T) {
	name := field.NewString("name").At("owner")
	if name.Name() != "owner.name" || name.Contains(".*").Err != nil {
		t.Fatal("字符串字段丢失别名或操作")
	}
	if field.NewNumeric[int64]("count").At("owner").Inc(1).Path != "owner.count" {
		t.Fatal("数值字段丢失别名")
	}
	if field.NewArray[int64]("ids").At("owner").Push(1).Path != "owner.ids" {
		t.Fatal("数组字段丢失别名")
	}
}

func TestFluentQueryPreservesScopesAndIndependentBuilders(t *testing.T) {
	root, foreign, _ := querySources(t)
	root.config.TenantField = "company_id"
	foreign.config.TenantField = "company_id"
	ctx := WithTenant(context.Background(), int64(9))
	owner, id := field.New[int64]("owner_id"), field.New[int64]("_id")
	chain := root.WithContext(ctx).LeftJoin(foreign, owner, id, "owner").Where(id.Eq(1))
	want, err := From(root).LeftJoin(foreign, owner, id, "owner").Where(id.Eq(1)).Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := chain.Build(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("链式入口改变了关联或隔离条件: %v %v", got, err)
	}
	if chain.ctx != ctx {
		t.Fatal("链式查询丢失原始上下文")
	}
	other := root.WithContext(context.Background())
	if _, err := other.Build(other.ctx); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("独立查询复用了租户: %v", err)
	}
	rebound := chain.WithContext(context.Background()).Where(id.Eq(2)).Order(id.Asc())
	if chain.ctx != ctx || len(chain.steps) != 2 || len(rebound.steps) != 3 || len(chain.option.sort) != 0 {
		t.Fatal("绑定新上下文污染原查询")
	}
}

func TestFluentQueryRejectsMissingContextAndInvalidDestinations(t *testing.T) {
	root, _, _ := querySources(t)
	var rows []queryEntity
	for _, q := range []*Aggregation{nil, From(root), root.WithContext(nil)} {
		if err := q.Scan(&rows); err == nil {
			t.Fatal("无上下文的 Scan 未报错")
		}
		if err := q.ScanOne(&queryEntity{}); err == nil {
			t.Fatal("无上下文的 ScanOne 未报错")
		}
		if _, err := q.ScanByPage(&rows, 1, 10); err == nil {
			t.Fatal("无上下文的分页未报错")
		}
	}
	q := root.WithContext(context.Background())
	var nilSlice *[]queryEntity
	for _, dest := range []any{nil, nilSlice, rows, &queryEntity{}, new(int)} {
		if err := q.Scan(dest); err == nil {
			t.Fatalf("Scan 接受无效目标 %T", dest)
		}
		if _, err := q.ScanByPage(dest, 1, 10); err == nil {
			t.Fatalf("分页接受无效目标 %T", dest)
		}
	}
	var nilRow *queryEntity
	for _, dest := range []any{nil, nilRow, queryEntity{}, &rows, new(int)} {
		if err := q.ScanOne(dest); err == nil {
			t.Fatalf("ScanOne 接受无效目标 %T", dest)
		}
	}
}
