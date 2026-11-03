//go:build mongodb_integration

package mongodatasets

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/mongodbfixture"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// This is a separate native root so callers need not replay the accepted
// mutation/getMore fault root. All malformed values are written only by the
// synthetic fixture administrator, never by the production source principal.
func TestMongoDBNativeMalformedRowsAndBudgetsDiscardCompleteExport(t *testing.T) {
	f := mongodbfixture.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source := f.Source
	policy := config.DefaultMongoDBConfiguration()
	policy.Enabled, policy.BatchSize = true, 2
	mapping := Mapping{Prompt: "question", Completion: "answer", Conversion: "string"}
	limits := config.DefaultDatasetPolicy().Limits
	original, err := Read(ctx, source, policy, mapping, limits, false)
	if err != nil || original.Rows != 8 {
		t.Fatal("exact native seed unavailable", err)
	}
	collection := f.Admin.Database(source.Database).Collection(source.Collection)
	cursor, err := collection.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	var documents []bson.D
	if err = cursor.All(ctx, &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 8 {
		t.Fatal("exact eight-row ownership required")
	}
	last := mustID(t, original.LastID)
	restore := func(t *testing.T) {
		t.Helper()
		bounded, stop := context.WithTimeout(context.Background(), time.Duration(f.Input.Policy.QuerySeconds)*time.Second)
		defer stop()
		for _, document := range documents {
			var id any
			for _, field := range document {
				if field.Key == "_id" {
					id = field.Value
				}
			}
			result, e := collection.ReplaceOne(bounded, bson.D{{Key: "_id", Value: id}}, document)
			if e != nil || result.MatchedCount != 1 {
				t.Fatal("restore exact owned seed", e)
			}
		}
	}
	refuse := func(t *testing.T, selected wire.Limits, mode, code string) {
		t.Helper()
		if selected.Validate() != nil {
			t.Fatal("invalid test limit policy")
		}
		m := mapping
		m.Conversion = mode
		result, e := Read(ctx, source, policy, m, selected, false)
		if e == nil || e.Error() != code || result.Rows != 0 || len(result.Data) != 0 || result.FirstID != "" || result.LastID != "" {
			t.Fatalf("partial/error result escaped: rows=%d error=%v", result.Rows, e)
		}
	}
	for _, tc := range []struct {
		name   string
		update bson.D
		code   string
	}{
		{"missing", bson.D{{Key: "$unset", Value: bson.D{{Key: "answer", Value: ""}}}}, "MONGODB_FIELD_INVALID"},
		{"null", bson.D{{Key: "$set", Value: bson.D{{Key: "answer", Value: nil}}}}, "MONGODB_FIELD_INVALID"},
		{"number_requires_explicit_conversion", bson.D{{Key: "$set", Value: bson.D{{Key: "answer", Value: int64(9007199254740993)}}}}, "MONGODB_TYPE_INVALID"},
		{"empty_text", bson.D{{Key: "$set", Value: bson.D{{Key: "answer", Value: ""}}}}, "MONGODB_RECORD_INVALID"},
		{"text_size", bson.D{{Key: "$set", Value: bson.D{{Key: "answer", Value: strings.Repeat("x", int(limits.MaximumTextBytes)+1)}}}}, "MONGODB_RECORD_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { restore(t) })
			result, e := collection.UpdateOne(ctx, bson.D{{Key: "_id", Value: last}}, tc.update)
			if e != nil || result.ModifiedCount != 1 {
				t.Fatal("mutate last synthetic record", e)
			}
			refuse(t, limits, "string", tc.code)
		})
	}
	t.Run("mixed_identifier", func(t *testing.T) {
		if _, e := collection.InsertOne(ctx, bson.D{{Key: "_id", Value: true}, {Key: "question", Value: "synthetic mixed identifier"}, {Key: "answer", Value: "must refuse"}}); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			bounded, stop := context.WithTimeout(context.Background(), time.Duration(f.Input.Policy.QuerySeconds)*time.Second)
			defer stop()
			removed, e := collection.DeleteOne(bounded, bson.D{{Key: "_id", Value: true}})
			if e != nil || removed.DeletedCount != 1 {
				t.Fatal("remove exact synthetic bool identifier", e)
			}
		})
		refuse(t, limits, "string", "MONGODB_IDENTIFIER_INVALID")
	})
	t.Run("row_budget", func(t *testing.T) {
		selected := limits
		selected.MaximumRows = 3
		selected.MaximumErrors = 3
		refuse(t, selected, "string", "MONGODB_LIMIT_EXCEEDED")
	})
	t.Run("byte_budget", func(t *testing.T) {
		t.Cleanup(func() { restore(t) })
		modified, e := collection.UpdateMany(ctx, bson.D{}, bson.D{{Key: "$set", Value: bson.D{{Key: "question", Value: strings.Repeat("p", 256)}}}})
		if e != nil || modified.MatchedCount != 8 {
			t.Fatal("prepare valid long synthetic records", e)
		}
		selected := limits
		selected.MaximumBytes = 1024
		selected.MaximumLineBytes = 1024
		selected.MaximumTextBytes = 256
		refuse(t, selected, "string", "MONGODB_LIMIT_EXCEEDED")
	})
	t.Run("canonical_int64_precision", func(t *testing.T) {
		t.Cleanup(func() { restore(t) })
		if _, e := collection.UpdateOne(ctx, bson.D{{Key: "_id", Value: last}}, bson.D{{Key: "$set", Value: bson.D{{Key: "answer", Value: int64(9007199254740993)}}}}); e != nil {
			t.Fatal(e)
		}
		m := mapping
		m.Conversion = "extended_json"
		result, e := Read(ctx, source, policy, m, limits, false)
		if e != nil || result.Rows != 8 {
			t.Fatal("explicit actual extended JSON read", e)
		}
		lines := bytes.Split(bytes.TrimSpace(result.Data), []byte{'\n'})
		record, e := wire.DecodeRecord(lines[len(lines)-1], limits)
		if e != nil || record.SourceID != last.Hex() || record.Completion != `{"$numberLong":"9007199254740993"}` {
			t.Fatal("actual int64 precision changed", e)
		}
	})
	restored, err := Read(ctx, source, policy, mapping, limits, false)
	if err != nil || !bytes.Equal(restored.Data, original.Data) {
		t.Fatal("exact fresh seed read after refusals", err)
	}
}
