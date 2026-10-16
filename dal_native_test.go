package mongoplus

import (
	"math"
	"testing"
	"testing/fstest"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func testDALFiles(files map[string]string) *DAL {
	fs := fstest.MapFS{}
	for name, text := range files {
		fs[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return &DAL{loader: NewEmbedLoader(fs)}
}

func TestDALNativeFileValidation(t *testing.T) {
	files := map[string]string{
		"good.json":      `{"collection":"users","filter":{"name":"@name"},"options":{"batchSize":2}}`,
		"bulk.json":      `{"collection":"users","op":"bulkWrite","operations":[{"op":"insertOne","document":{"_id":"?"}},{"op":"updateOne","filter":{"_id":"?"},"update":[{"$set":{"status":1}}],"upsert":true},{"op":"updateMany","filter":{"status":0},"update":{"$set":{"status":1}}},{"op":"replaceOne","filter":{"_id":3},"replacement":{"status":2}},{"op":"deleteOne","filter":{"_id":4}},{"op":"deleteMany","filter":{"status":3}}]}`,
		"missing.json":   `{"collection":"users","op":"bulkWrite","operations":[{"op":"updateMany","update":{"$set":{"status":0}}}]}`,
		"wrong.json":     `{"collection":"users","op":"bulkWrite","operations":[{"op":"deleteOne","filter":{"_id":1},"upsert":true}]}`,
		"unknown.json":   `{"collection":"users","op":"watch","options":{"fullDocumnt":"updateLookup"}}`,
		"duplicate.json": `{"collection":"users","op":"find","op":"deleteMany","allowAll":true}`,
		"reserved.json":  `{"collection":"__mongo_plus_migrations"}`,
		"typo.json":      `{"collection":"users","filters":{"tenant_id":1}}`,
	}
	dal := testDALFiles(files)
	value := `x"},"$where":"evil`
	command, err := dal.command("good.json", nil, map[string]any{"name": value})
	if err != nil || command.filter[0].Value != value {
		t.Fatalf("typed binding: %+v %v", command, err)
	}
	command, err = dal.command("bulk.json", []any{int64(1), int64(2)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	models, err := dalBulkModels(command)
	if err != nil || len(models) != 6 {
		t.Fatalf("models: %v %v", models, err)
	}
	for _, name := range []string{"missing.json", "wrong.json"} {
		command, err := dal.command(name, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dalBulkModels(command); err == nil {
			t.Fatal("accepted unsafe model", name)
		}
	}
	for _, name := range []string{"duplicate.json", "reserved.json", "typo.json"} {
		if _, err := dal.command(name, nil, nil); err == nil {
			t.Fatal("accepted invalid file", name)
		}
	}
	command, err = dal.command("unknown.json", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dalWatchOptions(command.options); err == nil {
		t.Fatal("accepted misspelled watch option")
	}
	if _, err = dalPipeline(dalCommand{pipeline: bson.A{bson.D{{Key: "$merge", Value: "target"}}}}, false); err == nil {
		t.Fatal("read accepted write stage")
	}
	for _, value := range []any{1.5, math.NaN(), math.Inf(1), float64(0x1p63)} {
		if _, err := dalInt(value); err == nil {
			t.Fatal("accepted noninteger", value)
		}
	}
	if _, err := dalWatchOptions(bson.D{{Key: "resumeAfter", Value: bson.D{{Key: "_data", Value: "a"}}}, {Key: "startAfter", Value: bson.D{{Key: "_data", Value: "b"}}}}); err == nil {
		t.Fatal("accepted conflicting resume options")
	}
	if _, err := dalWatchOptions(bson.D{{Key: "fullDocumentBeforeChange", Value: "updateLookup"}}); err == nil {
		t.Fatal("accepted wrong preimage mode")
	}
}
