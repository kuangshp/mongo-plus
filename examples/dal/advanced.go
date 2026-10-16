package dalexample

import (
	"context"
	"fmt"
	"io"

	mp "github.com/kuangshp/mongo-plus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// UploadAsset 把 GridFS 封装为业务仓储方法，Metadata 的租户来自认证上下文。
// GridFS 不支持多文档事务；此例明确拒绝绑定 Session，部分上传失败仍失效两个集合缓存。
func (r *ReportRepository) UploadAsset(ctx context.Context, filename string, reader io.Reader) (id bson.ObjectID, err error) {
	if filename == "" || reader == nil {
		return id, fmt.Errorf("文件名和内容不能为空")
	}
	err = r.base.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		if mongo.SessionFromContext(queryCtx) != nil {
			return fmt.Errorf("GridFS 上传不支持事务/Session")
		}
		params, err := tenantParams(queryCtx)
		if err != nil {
			return err
		}
		return mp.DALNative(queryCtx, []string{"assets.files", "assets.chunks"}, func(nativeCtx context.Context, db *mongo.Database) error {
			bucket := db.GridFSBucket(options.GridFSBucket().SetName("assets"))
			id, err = bucket.UploadFromStream(nativeCtx, filename, reader, options.GridFSUpload().SetMetadata(bson.D{
				{Key: "company_id", Value: params["companyID"]},
			}))
			return err
		})
	})
	return id, err
}

// DownloadAsset 先按租户检查文件 Metadata，再读取内容；客户端 fileID 不是授权凭据。
// 实际项目可在此继续叠加项目、部门、所有者等业务权限。
func (r *ReportRepository) DownloadAsset(ctx context.Context, id bson.ObjectID, writer io.Writer) (size int64, err error) {
	if id.IsZero() || writer == nil {
		return 0, fmt.Errorf("文件 ID 和输出不能为空")
	}
	err = r.base.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		if mongo.SessionFromContext(queryCtx) != nil {
			return fmt.Errorf("GridFS 下载不支持事务/Session")
		}
		params, err := tenantParams(queryCtx)
		if err != nil {
			return err
		}
		return mp.DALNative(queryCtx, nil, func(nativeCtx context.Context, db *mongo.Database) error {
			bucket := db.GridFSBucket(options.GridFSBucket().SetName("assets"))
			if err := bucket.GetFilesCollection().FindOne(nativeCtx, bson.D{
				{Key: "_id", Value: id},
				{Key: "metadata.company_id", Value: params["companyID"]},
			}).Err(); err != nil {
				return err
			}
			size, err = bucket.DownloadToStream(nativeCtx, id, writer)
			return err
		})
	})
	return size, err
}
