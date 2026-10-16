package dalexample

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/internal/testutil"
)

func TestGridFSNativeRepositoryTenantBoundary(t *testing.T) {
	db := mp.NewDatabase(testutil.Database(t))
	r, err := NewReportRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	one := mp.WithTenantID(context.Background(), int64(1))
	two := mp.WithTenantID(context.Background(), int64(2))
	id, err := r.UploadAsset(one, "review.txt", strings.NewReader("native repository extension"))
	if err != nil {
		t.Fatal(err)
	}
	var result bytes.Buffer
	if _, err = r.DownloadAsset(two, id, &result); !errors.Is(err, mp.ErrNotFound) || result.Len() != 0 {
		t.Fatalf("其他租户读到了文件: %v %q", err, result.String())
	}
	if _, err = r.DownloadAsset(one, id, &result); err != nil || result.String() != "native repository extension" {
		t.Fatalf("下载失败 %v %q", err, result.String())
	}
}
