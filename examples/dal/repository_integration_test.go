package dalexample_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	dalexample "github.com/kuangshp/mongo-plus/examples/dal"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestEmbeddedDALRepository(t *testing.T) {
	db := mp.NewDatabase(testutil.Database(t))
	ctx := mp.WithTenantID(context.Background(), int64(1))
	deleted := time.Now()
	_, err := db.Native().Collection("users").InsertMany(ctx, []dalexample.User{
		{ID: 1, CompanyID: 1, DeptID: 10, Username: "Alice"},
		{ID: 2, CompanyID: 1, DeptID: 20, Username: "Bob"},
		{ID: 3, CompanyID: 2, DeptID: 10, Username: "Alice"},
		{ID: 4, CompanyID: 1, DeptID: 10, Username: "deleted", DeletedAt: &deleted},
		{ID: 5, CompanyID: 1, DeptID: 10, Username: "alice"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Native().Collection("departments").InsertMany(ctx, []bson.D{
		{{Key: "_id", Value: int64(10)}, {Key: "company_id", Value: int64(1)}, {Key: "name", Value: "研发"}, {Key: "deleted_at", Value: nil}},
		{{Key: "_id", Value: int64(20)}, {Key: "company_id", Value: int64(2)}, {Key: "name", Value: "其他租户机密"}, {Key: "deleted_at", Value: nil}},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := dalexample.NewReportRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	page, err := repo.Summary(ctx, 1, 10)
	if err != nil || page.Total != 2 || len(page.List) != 2 || page.List[0].Users != 2 || page.List[1].DepartmentName != "未分配" {
		t.Fatalf("summary %+v %v", page, err)
	}
	if _, err := repo.Summary(context.Background(), 1, 10); !errors.Is(err, mp.ErrTenantRequired) {
		t.Fatalf("missing tenant %v", err)
	}
	count := 0
	if err := repo.Export(ctx, func(row dalexample.User) error {
		count++
		if row.CompanyID != 1 || row.DeletedAt != nil {
			t.Fatal(row)
		}
		return nil
	}); err != nil || count != 3 {
		t.Fatalf("export %d %v", count, err)
	}
	rows, err := repo.FindNameIgnoringCase(ctx, "ALICE")
	if err != nil || len(rows) != 2 {
		t.Fatalf("native %+v %v", rows, err)
	}
	result, err := repo.BulkStatus(ctx, 1, 2)
	if err != nil || result.MatchedCount != 2 {
		t.Fatalf("bulk %+v %v", result, err)
	}
	rollback := errors.New("rollback")
	err = mp.Transaction(ctx, db, func(tx context.Context) error {
		if _, err := repo.WithTx(tx).BulkStatus(ctx, 2, 1); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var one dalexample.User
	if err := db.Native().Collection("users").FindOne(ctx, bson.D{{Key: "_id", Value: int64(1)}}).Decode(&one); err != nil || one.Status != 1 {
		t.Fatalf("rollback %+v %v", one, err)
	}
	stream, err := repo.WatchActive(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close(context.Background())
	_, err = db.Native().Collection("users").InsertOne(ctx, dalexample.User{ID: 6, CompanyID: 1, Username: "new"})
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !stream.Next(wait) {
		t.Fatal("missing event", stream.Err())
	}
}
