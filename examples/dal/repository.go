// Package dalexample 演示启动初始化 → JSON 文件 → 业务仓储 → 事务/订阅的完整调用。
// 业务项目可将 query 目录复制到 internal/dal/query，将方法合并进非生成仓储文件。
package dalexample

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/examples/dal/generated/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

//go:embed query
var queryFiles embed.FS

// User 复用 schema/user.json 生成的实体；业务仓储不再维护第二份数据模型。
type User = model.UserEntity

// DepartmentSummary 是聚合投影结果，不要求与实体字段一致。
type DepartmentSummary struct {
	DepartmentID   int64  `bson:"department_id" json:"departmentId"`
	DepartmentName string `bson:"department_name" json:"departmentName"`
	Users          int64  `bson:"users" json:"users"`
}

// ReportRepository 对业务层只暴露业务方法，底层查询文件名不由请求传入。
type ReportRepository struct {
	base *mp.Repository[User, int64]
	dal  *mp.DAL
}

// NewReportRepository 使用启动入口已经创建的数据库，初始化嵌入文件并检查路径。
func NewReportRepository(db *mp.Database) (*ReportRepository, error) {
	files, err := fs.Sub(queryFiles, "query")
	if err != nil {
		return nil, err
	}
	dal, err := mp.NewDal(db, mp.NewEmbedLoader(files))
	if err != nil {
		return nil, err
	}
	if err := dal.Preload("user/summary.json", "user/count_summary.json", "user/export.json", "user/bulk_status.json", "user/watch.json"); err != nil {
		dal.Close()
		return nil, err
	}
	base, err := mp.NewRepository[User, int64](db, mp.CollectionConfig{
		Name:        "users",
		ID:          mp.IDConfig{Type: mp.IDTypeInt64, Generation: mp.IDGenerationManual},
		SoftDelete:  true,
		TenantField: "company_id",
	})
	if err != nil {
		dal.Close()
		return nil, err
	}
	return &ReportRepository{base: base, dal: dal}, nil
}

// Close 仅停止 DAL 自身的文件缓存清理，不关闭共享数据库连接。
func (r *ReportRepository) Close() { r.dal.Close() }

// WithTx 保留业务 DAL 依赖，沿用基础仓储的事务绑定检查。
func (r *ReportRepository) WithTx(tx context.Context) *ReportRepository {
	return &ReportRepository{base: r.base.WithTx(tx), dal: r.dal}
}

func tenantParams(ctx context.Context) (map[string]any, error) {
	id, ok := mp.TenantIDFromCtx[int64](ctx)
	if !ok || id <= 0 {
		return nil, mp.ErrTenantRequired
	}
	return map[string]any{"companyID": id}, nil
}

// Summary 使用精确分组计数和分页文件；权限参数来自服务端 Context。
func (r *ReportRepository) Summary(ctx context.Context, page, size int64) (mp.DALPageResult[DepartmentSummary], error) {
	var result mp.DALPageResult[DepartmentSummary]
	err := r.base.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		params, err := tenantParams(queryCtx)
		if err != nil {
			return err
		}
		offset, limit := mp.DbPage(page, size)
		params["offset"], params["limit"] = offset, limit
		result, err = mp.DALQueryPageNamed[DepartmentSummary](queryCtx, "user/summary.json", params)
		return err
	})
	return result, err
}

// Export 逐条处理结果，回调报错会结束读取并关闭游标。
func (r *ReportRepository) Export(ctx context.Context, consume func(User) error) error {
	return r.base.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		params, err := tenantParams(queryCtx)
		if err != nil {
			return err
		}
		return mp.DALWalkNamed(queryCtx, "user/export.json", params, consume)
	})
}

// BulkStatus 演示两个同集合更新模型；有事务需求时用 WithTx 后调用。
func (r *ReportRepository) BulkStatus(ctx context.Context, enabledID, disabledID int64) (*mongo.BulkWriteResult, error) {
	if enabledID <= 0 || disabledID <= 0 || enabledID == disabledID {
		return nil, fmt.Errorf("两个用户 ID 必须是不同的正整数")
	}
	var result *mongo.BulkWriteResult
	err := r.base.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		params, err := tenantParams(queryCtx)
		if err != nil {
			return err
		}
		params["enabledID"], params["disabledID"], params["now"] = enabledID, disabledID, time.Now()
		result, err = mp.DALBulkWriteNamed(queryCtx, "user/bulk_status.json", params)
		return err
	})
	return result, err
}

// WatchActive 仅订阅本租户仍可见文档的新增/更新/替换；不包含物理删除事件。
// 返回官方变更流，调用方负责持久化消费进度、处理重复事件与关闭资源。
func (r *ReportRepository) WatchActive(ctx context.Context, resumeToken bson.Raw) (*mongo.ChangeStream, error) {
	var stream *mongo.ChangeStream
	err := r.base.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		params, err := tenantParams(queryCtx)
		if err != nil {
			return err
		}
		params["resumeToken"] = nil
		if len(resumeToken) > 0 {
			params["resumeToken"] = resumeToken
		}
		stream, err = mp.DALWatchNamed(queryCtx, "user/watch.json", params)
		return err
	})
	return stream, err
}

// FindNameIgnoringCase 演示文件选项未覆盖时，仍在 DAL/Repository 层调用官方 API。
func (r *ReportRepository) FindNameIgnoringCase(ctx context.Context, name string) ([]User, error) {
	rows := []User{}
	err := r.base.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		params, err := tenantParams(queryCtx)
		if err != nil {
			return err
		}
		return mp.DALNative(queryCtx, nil, func(nativeCtx context.Context, db *mongo.Database) error {
			cursor, err := db.Collection("users").Find(nativeCtx, bson.D{
				{Key: "company_id", Value: params["companyID"]},
				{Key: "deleted_at", Value: nil},
				{Key: "username", Value: name},
			}, options.Find().SetCollation(&options.Collation{Locale: "en", Strength: 2}))
			if err != nil {
				return err
			}
			defer cursor.Close(context.WithoutCancel(nativeCtx))
			return cursor.All(nativeCtx, &rows)
		})
	})
	return rows, err
}
