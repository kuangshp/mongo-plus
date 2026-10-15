package mongoplus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type ackDocument struct {
	ID        bson.ObjectID `bson:"_id"`
	Name      string        `bson:"name"`
	DeletedAt *time.Time    `bson:"deleted_at"`
}

func TestAllWritesReportUnacknowledgedResults(t *testing.T) {
	native := testutil.Database(t)
	ack := mp.NewDatabase(native)
	unack := mp.NewDatabase(native.Client().Database(native.Name(), options.Database().SetWriteConcern(&writeconcern.WriteConcern{W: 0})))
	ctx := context.Background()
	type repo = mp.Repository[ackDocument, bson.ObjectID]
	for name, operation := range map[string]func(*repo, bson.ObjectID) error{
		"update_id": func(r *repo, id bson.ObjectID) error {
			_, err := r.UpdateById(ctx, id, field.NewString("name").Set("new"))
			return err
		},
		"update_map": func(r *repo, id bson.ObjectID) error {
			_, err := r.UpdateMapById(ctx, id, map[string]any{"name": "new"})
			return err
		},
		"update_wrapper": func(r *repo, id bson.ObjectID) error {
			_, err := r.UpdateByWrapper(ctx, func(w *mp.Wrapper) { w.Where(field.New[bson.ObjectID]("_id").Eq(id)) }, field.NewString("name").Set("new"))
			return err
		},
		"delete_id": func(r *repo, id bson.ObjectID) error { _, err := r.DeleteById(ctx, id); return err },
		"delete_ids": func(r *repo, id bson.ObjectID) error {
			_, err := r.DeleteByIdList(ctx, []bson.ObjectID{id})
			return err
		},
		"delete_condition": func(r *repo, id bson.ObjectID) error {
			_, err := r.DeleteByCondition(ctx, field.New[bson.ObjectID]("_id").Eq(id))
			return err
		},
		"delete_wrapper": func(r *repo, id bson.ObjectID) error {
			_, err := r.DeleteByWrapper(ctx, func(w *mp.Wrapper) { w.Where(field.New[bson.ObjectID]("_id").Eq(id)) })
			return err
		},
		"hard_delete_id": func(r *repo, id bson.ObjectID) error { _, err := r.HardDeleteById(ctx, id); return err },
		"hard_delete_wrapper": func(r *repo, id bson.ObjectID) error {
			_, err := r.HardDeleteByWrapper(ctx, func(w *mp.Wrapper) { w.Where(field.New[bson.ObjectID]("_id").Eq(id)) })
			return err
		},
		"restore": func(r *repo, id bson.ObjectID) error { _, err := r.RestoreById(ctx, id); return err },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := mp.CollectionConfig{Name: name, SoftDelete: true}
			writer := mp.MustRepository[ackDocument, bson.ObjectID](ack, cfg)
			r := mp.MustRepository[ackDocument, bson.ObjectID](unack, cfg)
			row := &ackDocument{Name: "old"}
			if err := writer.Create(ctx, row); err != nil {
				t.Fatal(err)
			}
			if name == "restore" {
				if _, err := writer.DeleteById(ctx, row.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := operation(r, row.ID); !errors.Is(err, mp.ErrUnacknowledgedWrite) {
				t.Fatalf("未确认写入返回 %v", err)
			}
		})
	}
	r := mp.MustRepository[ackDocument, bson.ObjectID](ack, mp.CollectionConfig{Name: "confirmed", SoftDelete: true})
	if n, err := r.DeleteById(ctx, bson.NewObjectID()); err != nil || n != 0 {
		t.Fatalf("已确认未命中应为 0,nil: %d %v", n, err)
	}
	if result, err := r.UpdateById(ctx, bson.NewObjectID(), field.NewString("name").Set("new")); err != nil || !result.Acknowledged || result.MatchedCount != 0 {
		t.Fatalf("已确认更新未命中: %+v %v", result, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.UpdateById(canceled, bson.NewObjectID(), field.NewString("name").Set("new")); !errors.Is(err, context.Canceled) {
		t.Fatalf("原始驱动错误丢失: %v", err)
	}
}
