package mongoplus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/internal/testutil"
)

type sensitiveCacheEntity struct {
	ID          int64  `bson:"_id"`
	Phone       string `bson:"-"`
	PhoneCipher string `bson:"phone_cipher"`
	PhoneIndex  string `bson:"phone_index"`
}

func TestSensitiveCacheUsesCurrentRequestMode(t *testing.T) {
	type permissionKey struct{}
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			db := NewDatabase(testutil.Database(t))
			if shared {
				db.UseCache(NewMemoryCache())
			}
			phone := PhoneField("Phone")
			phone.EncryptAtRest = true
			phone.ReturnModeResolver = func(ctx context.Context) SensitiveReturnMode {
				if allowed, _ := ctx.Value(permissionKey{}).(bool); allowed {
					return SensitiveReturnPlain
				}
				return SensitiveReturnMasked
			}
			if _, err := RegisterSensitive(db, SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{phone}}); err != nil {
				t.Fatal(err)
			}
			r := MustRepository[sensitiveCacheEntity, int64](db, CollectionConfig{Name: "users", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
			ctx := context.Background()
			if err := r.Create(ctx, &sensitiveCacheEntity{ID: 1, Phone: "13812345678"}); err != nil {
				t.Fatal(err)
			}
			option := Query().WithCache(time.Minute).Build()
			var reads atomic.Int32
			db.Observer = func(_ context.Context, info Observation) {
				if info.Operation == "FindOne" || info.Operation == "FindList" {
					reads.Add(1)
				}
			}
			plainContext := context.WithValue(ctx, permissionKey{}, true)
			for _, mode := range []struct {
				ctx  context.Context
				want string
			}{
				{plainContext, "13812345678"},
				{ctx, "138****5678"},
				{WithSensitiveMasked(plainContext), "138****5678"},
				{WithSensitivePlaintext(ctx), "13812345678"},
			} {
				one, err := r.FindById(mode.ctx, 1, option)
				if err != nil || one.Phone != mode.want {
					t.Fatalf("单条缓存返回模式: %+v, %v, want=%s", one, err, mode.want)
				}
				list, err := r.FindList(mode.ctx, option)
				if err != nil || len(list) != 1 || list[0].Phone != mode.want {
					t.Fatalf("列表缓存返回模式: %+v, %v, want=%s", list, err, mode.want)
				}
			}
			cipher, err := r.FindById(WithSensitiveCiphertext(ctx), 1, option)
			if err != nil || cipher.Phone != cipher.PhoneCipher || cipher.Phone == "13812345678" {
				t.Fatalf("密文模式: %+v, %v", cipher, err)
			}
			if reads.Load() != 2 {
				t.Fatalf("应复用原始缓存，只读取一次单条和一次列表，实际 %d 次", reads.Load())
			}
		})
	}
}
