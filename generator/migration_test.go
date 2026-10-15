package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 在独立业务 module 内编译迁移用法，确认新方法通过生成接口可访问。
func TestGeneratedGormPlusMigrationUsage(t *testing.T) {
	c, dir := fixture(t)
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "user.json"), []byte(`{"fields":[{"name":"username","type":"string"},{"name":"status","type":"int64"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(c); err != nil {
		t.Fatal(err)
	}
	usage := fmt.Sprintf(`package migration

import (
    "context"
    "time"
    mongoplus "github.com/kuangshp/mongo-plus"
    dao %q
    repository %q
)

func migratedRepositoryCalls(ctx context.Context, db *mongoplus.Database, users repository.UserRepository) error {
    _ = repository.NewUserRepository()
    _ = repository.NewUserRepository(db)
    _, _, err := users.FindPageByWrapper(ctx, 1, 10, func(w *mongoplus.Wrapper) {
        w.WhereGroupFn(func(group *mongoplus.Wrapper) {
            group.Like(dao.UserEntity.Username, "admin").OrRLike(dao.UserEntity.Username, "test_")
        }).BetweenIfNotZero(dao.UserEntity.CreatedAt, time.Now().Add(-time.Hour), time.Now()).
            OrderTriState("desc", dao.UserEntity.ID.Asc(), dao.UserEntity.ID.Desc()).
            OrderDefault(dao.UserEntity.ID.Asc())
    }, mongoplus.QueryOpt().WithSingleFlight(0).Build())
    if err != nil { return err }
    _, err = users.CountByWrapper(ctx, nil, mongoplus.QueryOpt().WithUnscoped().Build())
    if err != nil { return err }
    _, err = users.ExistsByWrapper(ctx, nil, mongoplus.QueryOpt().Where(dao.UserEntity.Status.Eq(0)).Build())
    if err != nil { return err }
    _, err = users.UpdateById(ctx, 1, mongoplus.Update().WithColumns(dao.UserEntity.Status.Value(0)).Build())
    if err != nil { return err }
    _, err = users.UpdateById(ctx, 1, mongoplus.WithUpdateColumns(dao.UserEntity.Status.Set(1)))
    if err != nil { return err }
    _, err = users.DeleteByIdList(ctx, []int64{1, 2}, mongoplus.Delete().WithPhysicalDelete().Build())
    if err != nil { return err }
    _, err = users.DeleteByCondition(ctx, dao.UserEntity.Status.Eq(0))
    return err
}
`, c.ImportPath("dao"), c.ImportPath("repository"))
	if err := os.WriteFile(filepath.Join(dir, "migration_test.go"), []byte(usage), 0600); err != nil {
		t.Fatal(err)
	}
	compileFixture(t, dir)
}
