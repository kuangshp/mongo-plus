package mongoplus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/examples/query/dao"
	"github.com/kuangshp/mongo-plus/examples/query/model"
	exampleRepo "github.com/kuangshp/mongo-plus/examples/query/repository"
	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var _ mp.Source = (exampleRepo.UserRepository)(nil)

type JoinUser struct {
	ID        int64      `bson:"_id"`
	Name      string     `bson:"name"`
	DeptID    *int64     `bson:"dept_id"`
	RoleIDs   []int64    `bson:"role_ids"`
	CompanyID int64      `bson:"company_id"`
	DeletedAt *time.Time `bson:"deleted_at"`
}

type joinDept struct {
	ID        int64      `bson:"_id"`
	Name      string     `bson:"name"`
	ManagerID int64      `bson:"manager_id"`
	CompanyID int64      `bson:"company_id"`
	DeletedAt *time.Time `bson:"deleted_at"`
}

type joinRole struct {
	ID        int64      `bson:"_id"`
	Name      string     `bson:"name"`
	UserID    *int64     `bson:"user_id"`
	CompanyID int64      `bson:"company_id"`
	DeletedAt *time.Time `bson:"deleted_at"`
}

type joinedUser struct {
	JoinUser   `bson:",inline"`
	Department *joinDept  `bson:"department"`
	Manager    *JoinUser  `bson:"manager"`
	Roles      []joinRole `bson:"roles"`
	Role       *joinRole  `bson:"role"`
}

type joinFixture struct {
	db          *mp.Database
	users       *mp.Repository[JoinUser, int64]
	departments *mp.Repository[joinDept, int64]
	roles       *mp.Repository[joinRole, int64]
	ctx         context.Context
}

func joinedFixture(t *testing.T) joinFixture {
	t.Helper()
	db := mp.NewDatabase(testutil.Database(t))
	cfg := func(name string) mp.CollectionConfig {
		return mp.CollectionConfig{Name: name, ID: mp.IDConfig{Type: "int64", Generation: "manual"}, SoftDelete: true, TenantField: "company_id"}
	}
	f := joinFixture{db: db, users: mp.MustRepository[JoinUser, int64](db, cfg("users")), departments: mp.MustRepository[joinDept, int64](db, cfg("departments")), roles: mp.MustRepository[joinRole, int64](db, cfg("roles")), ctx: mp.WithTenant(context.Background(), int64(1))}
	other := mp.WithTenant(f.ctx, int64(2))
	pointer := func(id int64) *int64 { return &id }
	for _, d := range []*joinDept{{ID: 1, Name: "研发", ManagerID: 10}, {ID: 3, Name: "已删除部门"}} {
		if err := f.departments.Create(f.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.departments.Create(other, &joinDept{ID: 2, Name: "其他租户部门"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.departments.DeleteById(f.ctx, 3); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*JoinUser{{ID: 1, Name: "user1", DeptID: pointer(1), RoleIDs: []int64{11, 12}}, {ID: 2, Name: "cross", DeptID: pointer(2)}, {ID: 3, Name: "deleted-dept", DeptID: pointer(3)}, {ID: 4, Name: "no-dept"}, {ID: 6, Name: "deleted-user", DeptID: pointer(1)}, {ID: 10, Name: "manager"}} {
		if err := f.users.Create(f.ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.users.Create(other, &JoinUser{ID: 5, Name: "other-tenant", DeptID: pointer(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.users.DeleteById(f.ctx, 6); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*joinRole{{ID: 11, Name: "reader", UserID: pointer(1)}, {ID: 12, Name: "writer", UserID: pointer(1)}, {ID: 14, Name: "deleted-role", UserID: pointer(1)}, {ID: 15, Name: "null-user"}} {
		if err := f.roles.Create(f.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.roles.Create(other, &joinRole{ID: 13, Name: "other-tenant-role", UserID: pointer(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.roles.DeleteById(f.ctx, 14); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f joinFixture) base() *mp.Aggregation {
	return mp.From(f.users).Where(field.NewNumeric[int64]("_id").Lte(4)).Order(field.NewNumeric[int64]("_id").Asc())
}

func TestLeftInnerLookupAndIsolation(t *testing.T) {
	f := joinedFixture(t)
	left := f.base().LeftJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department")
	rows, err := mp.Scan[joinedUser](f.ctx, left)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].Department == nil || rows[0].Department.Name != "研发" {
		t.Fatalf("左关联错误 %+v", rows)
	}
	for _, row := range rows[1:] {
		if row.Department != nil {
			t.Fatalf("泄露其他租户/已删除/空关联: %+v", row)
		}
	}
	inner := f.base().InnerJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department")
	rows, err = mp.Scan[joinedUser](f.ctx, inner)
	if err != nil || len(rows) != 1 || rows[0].ID != 1 {
		t.Fatalf("内关联 %+v %v", rows, err)
	}
	lookup := f.base().Lookup(f.roles, field.New[int64]("_id"), field.New[int64]("user_id"), "roles", mp.Query().Order(field.New[int64]("_id").Asc()).Build())
	rows, err = mp.Scan[joinedUser](f.ctx, lookup)
	if err != nil || len(rows) != 4 || len(rows[0].Roles) != 2 {
		t.Fatalf("数组关联 %+v %v", rows, err)
	}
	if rows[0].Roles[0].ID != 11 || rows[0].Roles[1].ID != 12 {
		t.Fatalf("关联数组包含隐藏数据 %+v", rows[0].Roles)
	}
	for _, row := range rows[1:] {
		if len(row.Roles) != 0 {
			t.Fatalf("不应关联到其他记录 %+v", row)
		}
	}
}

func TestJoinFiltersProjectionAndChaining(t *testing.T) {
	f := joinedFixture(t)
	query := f.base().LeftJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department").
		LeftJoin(f.users, field.New[int64]("manager_id").At("department"), field.New[int64]("_id"), "manager").
		Where(field.NewString("name").At("department").Eq("研发")).
		Project(bson.D{{Key: "_id", Value: 0}, {Key: "username", Value: "$name"}, {Key: "departmentName", Value: "$department.name"}, {Key: "managerName", Value: "$manager.name"}})
	type row struct {
		Username       string `bson:"username"`
		DepartmentName string `bson:"departmentName"`
		ManagerName    string `bson:"managerName"`
	}
	rows, total, err := mp.ScanByPage[row](f.ctx, query, 1, 10)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Username != "user1" || rows[0].DepartmentName != "研发" || rows[0].ManagerName != "manager" {
		t.Fatalf("链式关联/VO 投影 %+v total=%d err=%v", rows, total, err)
	}
	noMatch := f.base().InnerJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department", mp.Query().Where(field.NewString("name").Eq("不存在")).Build())
	if _, err = mp.ScanOne[joinedUser](f.ctx, noMatch); !errors.Is(err, mp.ErrNotFound) {
		t.Fatalf("ScanOne 空结果 %v", err)
	}
}

func TestJoinPaginationCountsExpandedRows(t *testing.T) {
	f := joinedFixture(t)
	query := f.base().LeftJoin(f.roles, field.New[int64]("_id"), field.New[int64]("user_id"), "role")
	seen := []int64{}
	for page := int64(1); page <= 3; page++ {
		rows, total, err := mp.ScanByPage[joinedUser](f.ctx, query, page, 2)
		if err != nil || total != 5 {
			t.Fatalf("展开后的 total 应为 5，得到 %d err=%v", total, err)
		}
		for _, row := range rows {
			if row.Role != nil {
				seen = append(seen, row.Role.ID)
			} else {
				seen = append(seen, -row.ID)
			}
		}
	}
	want := []int64{11, 12, -2, -3, -4}
	if len(seen) != len(want) {
		t.Fatal(seen)
	}
	for i, id := range want {
		if seen[i] != id {
			t.Fatalf("分页顺序或行重复 %v", seen)
		}
	}
	rows, total, err := mp.ScanByPage[joinedUser](f.ctx, query, 99, 2)
	if err != nil || len(rows) != 0 || rows == nil || total != 5 {
		t.Fatalf("超出页数 %+v %d %v", rows, total, err)
	}
	array := f.base().Lookup(f.roles, field.New[int64]("_id"), field.New[int64]("user_id"), "roles")
	if _, total, err = mp.ScanByPage[joinedUser](f.ctx, array, 1, 2); err != nil || total != 4 {
		t.Fatalf("数组关联 total 应为主记录数量 %d %v", total, err)
	}
	empty := f.base().Where(field.New[int64]("_id").Eq(99))
	rows, total, err = mp.ScanByPage[joinedUser](f.ctx, empty, 1, 2)
	if err != nil || total != 0 || len(rows) != 0 || rows == nil {
		t.Fatalf("空分页 %+v %d %v", rows, total, err)
	}
}

func TestJoinScopesOverridesAndNullReferences(t *testing.T) {
	f := joinedFixture(t)
	withDeleted := f.base().LeftJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department", mp.Query().WithDeleted().Build())
	rows, err := mp.Scan[joinedUser](f.ctx, withDeleted)
	if err != nil || rows[2].Department == nil || rows[2].Department.ID != 3 {
		t.Fatalf("关联 WithDeleted %+v %v", rows, err)
	}
	if rows[1].Department != nil {
		t.Fatal("WithDeleted 绕过了租户")
	}
	mainDeleted := mp.From(f.users).OnlyDeleted().LeftJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department")
	rows, err = mp.Scan[joinedUser](f.ctx, mainDeleted)
	if err != nil || len(rows) != 1 || rows[0].ID != 6 {
		t.Fatalf("主集合 OnlyDeleted %+v %v", rows, err)
	}
	nullJoin := f.base().Where(field.New[int64]("_id").Eq(4)).Lookup(f.roles, field.New[int64]("dept_id"), field.New[int64]("user_id"), "roles")
	rows, err = mp.Scan[joinedUser](f.ctx, nullJoin)
	if err != nil || len(rows) != 1 || len(rows[0].Roles) != 0 {
		t.Fatalf("null 不应关联 null %+v %v", rows, err)
	}
	arrayJoin := f.base().Lookup(f.roles, field.NewArray[int64]("role_ids"), field.New[int64]("_id"), "roles")
	rows, err = mp.Scan[joinedUser](f.ctx, arrayJoin)
	if err != nil || len(rows[0].Roles) != 2 || len(rows[3].Roles) != 0 {
		t.Fatalf("数组主键关联 %+v %v", rows, err)
	}
	limited := f.base().Lookup(f.roles, field.New[int64]("_id"), field.New[int64]("user_id"), "roles", mp.Query().Order(field.New[int64]("_id").Desc()).Limit(1).Build())
	rows, err = mp.Scan[joinedUser](f.ctx, limited)
	if err != nil || len(rows[0].Roles) != 1 || rows[0].Roles[0].ID != 12 {
		t.Fatalf("每条主记录的关联 Limit %+v %v", rows, err)
	}
}

func TestJoinInsideTransaction(t *testing.T) {
	f := joinedFixture(t)
	abort := errors.New("rollback")
	err := mp.Transaction(f.ctx, f.db, func(txCtx context.Context) error {
		if _, err := f.departments.UpdateById(txCtx, 1, field.NewString("name").Set("事务内")); err != nil {
			return err
		}
		var row joinedUser
		err := f.base().WithContext(txCtx).Where(field.New[int64]("_id").Eq(1)).
			LeftJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department").ScanOne(&row)
		if err != nil {
			return err
		}
		if row.Department == nil || row.Department.Name != "事务内" {
			return errors.New("关联查询未使用事务上下文")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	got, err := f.departments.FindById(f.ctx, 1)
	if err != nil || got.Name != "研发" {
		t.Fatalf("事务未回滚 %+v %v", got, err)
	}
}

func TestFluentJoinExecution(t *testing.T) {
	f := joinedFixture(t)
	query := f.base().WithContext(f.ctx).LeftJoin(f.departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department")
	var rows []*joinedUser
	if err := query.Scan(&rows); err != nil || len(rows) != 4 || rows[0].Department == nil || rows[0].Department.Name != "研发" {
		t.Fatalf("链式关联列表: %+v %v", rows, err)
	}
	total, err := query.ScanByPage(&rows, 1, 2)
	if err != nil || total != 4 || len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("链式分页: %+v %d %v", rows, total, err)
	}
	total, err = query.ScanByPage(&rows, 9, 2)
	if err != nil || total != 4 || rows == nil || len(rows) != 0 {
		t.Fatalf("末页应清空旧结果并保留总数: %+v %d %v", rows, total, err)
	}
	var one joinedUser
	if err := query.ScanOne(&one); err != nil || one.ID != 1 || one.Department == nil {
		t.Fatalf("链式单条: %+v %v", one, err)
	}
	missing := f.users.WithContext(f.ctx).Where(field.New[int64]("_id").Eq(999))
	if err := missing.ScanOne(&one); !errors.Is(err, mp.ErrNotFound) || one.ID != 1 {
		t.Fatalf("未找到应报错且不覆盖旧值: %+v %v", one, err)
	}
	total, err = missing.ScanByPage(&rows, 1, 10)
	if err != nil || total != 0 || rows == nil || len(rows) != 0 {
		t.Fatalf("空分页: %+v %d %v", rows, total, err)
	}
	if err := missing.Scan(&rows); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("空列表: %+v %v", rows, err)
	}
	var nativeMap bson.M
	if err := f.users.WithContext(f.ctx).Where(field.New[int64]("_id").Eq(1)).ScanOne(&nativeMap); err != nil || nativeMap["_id"] != int64(1) {
		t.Fatalf("map 单条: %+v %v", nativeMap, err)
	}
	if err := query.WithContext(context.Background()).Scan(&rows); !errors.Is(err, mp.ErrTenantRequired) {
		t.Fatalf("链式执行未检查租户: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(f.ctx)
	cancel()
	if err := query.WithContext(cancelCtx).Scan(&rows); !errors.Is(err, context.Canceled) {
		t.Fatalf("丢失取消信号: %v", err)
	}
}

func TestGeneratedRepositoryFluentJoin(t *testing.T) {
	db := mp.NewDatabase(testutil.Database(t))
	ctx := context.Background()
	users := exampleRepo.NewUserRepository(db)
	departments := exampleRepo.NewDepartmentRepository(db)
	department := &model.DepartmentEntity{Name: "研发"}
	if err := departments.Create(ctx, department); err != nil {
		t.Fatal(err)
	}
	user := &model.UserEntity{Username: "fluent", Status: 1, DeptID: department.ID}
	if err := users.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	type row struct {
		model.UserEntity `bson:",inline"`
		Department       *model.DepartmentEntity `bson:"department"`
	}
	var rows []row
	total, err := users.WithContext(ctx).
		LeftJoin(departments, dao.UserEntity.DeptID, dao.DepartmentEntity.ID, "department").
		Where(dao.UserEntity.Status.Eq(1)).
		ScanByPage(&rows, 1, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Department == nil || rows[0].Department.Name != "研发" {
		t.Fatalf("生成接口无法链式关联: %+v %d %v", rows, total, err)
	}
}

func exerciseJoinID[K mp.ID](t *testing.T, config mp.IDConfig, first, second K) {
	t.Helper()
	db := mp.NewDatabase(testutil.Database(t))
	ctx := context.Background()
	type entity struct {
		ID  K `bson:"_id"`
		Ref K `bson:"ref"`
	}
	type row struct {
		ID     K       `bson:"_id"`
		Linked *entity `bson:"linked"`
	}
	r := mp.MustRepository[entity, K](db, mp.CollectionConfig{Name: "entities", ID: config})
	if err := r.Create(ctx, &entity{ID: first}); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, &entity{ID: second, Ref: first}); err != nil {
		t.Fatal(err)
	}
	query := mp.From(r).Where(field.New[K]("_id").Eq(second)).LeftJoin(r, field.New[K]("ref"), field.New[K]("_id"), "linked")
	got, err := mp.ScanOne[row](ctx, query)
	if err != nil || got.Linked == nil || got.Linked.ID != first {
		t.Fatalf("主键关联 %+v %v", got, err)
	}
}

func TestJoinIDTypes(t *testing.T) {
	t.Run("ObjectID", func(t *testing.T) {
		exerciseJoinID(t, mp.IDConfig{Type: "objectId", Generation: "auto"}, bson.NewObjectID(), bson.NewObjectID())
	})
	t.Run("string", func(t *testing.T) {
		exerciseJoinID(t, mp.IDConfig{Type: "string", Generation: "manual"}, "first", "second")
	})
}
