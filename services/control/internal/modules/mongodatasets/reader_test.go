package mongodatasets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func TestMongoDBConversionPreservesBSONTypes(t *testing.T) {
	limits := config.DefaultDatasetPolicy().Limits
	id := bson.NewObjectID()
	when := bson.NewDateTimeFromTime(time.Unix(1000, 0))
	decimal, _ := bson.ParseDecimal128("123.456")
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"int64", int64(9007199254740993), `{"$numberLong":"9007199254740993"}`},
		{"date", when, `{"$date":{"$numberLong":"1000000"}}`},
		{"decimal", decimal, `{"$numberDecimal":"123.456"}`},
		{"array", bson.A{int32(1), true}, `[{"$numberInt":"1"},true]`},
		{"document", bson.D{{Key: "a", Value: int32(2)}}, `{"a":{"$numberInt":"2"}}`},
		{"binary", bson.Binary{Subtype: 0, Data: []byte{1, 2}}, `{"$binary":{"base64":"AQI=","subType":"00"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, e := bson.Marshal(bson.D{{Key: "_id", Value: id}, {Key: "input", Value: bson.D{{Key: "text", Value: "hello"}}}, {Key: "answer", Value: tc.value}})
			if e != nil {
				t.Fatal(e)
			}
			out, e := Convert(raw, Mapping{"input.text", "answer", "extended_json"}, limits)
			if e != nil {
				t.Fatal(e)
			}
			record, e := wire.DecodeRecord(out, limits)
			if e != nil || record.Prompt != "hello" || record.Completion != tc.want || record.SourceID != id.Hex() {
				t.Fatalf("wrong conversion: %s, %v", out, e)
			}
		})
	}
}
func TestMongoDBConversionFailsClosed(t *testing.T) {
	limits := config.DefaultDatasetPolicy().Limits
	for _, tc := range []struct {
		name    string
		doc     bson.D
		mapping Mapping
		code    string
	}{
		{"unsupported-id", bson.D{{Key: "_id", Value: "a"}}, Mapping{"p", "c", "string"}, "MONGODB_IDENTIFIER_INVALID"},
		{"missing", bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "p", Value: "text"}}, Mapping{"p", "c", "string"}, "MONGODB_FIELD_INVALID"},
		{"null", bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "p", Value: "text"}, {Key: "c", Value: nil}}, Mapping{"p", "c", "extended_json"}, "MONGODB_FIELD_INVALID"},
		{"number-string-mode", bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "p", Value: "text"}, {Key: "c", Value: int64(42)}}, Mapping{"p", "c", "string"}, "MONGODB_TYPE_INVALID"},
		{"empty", bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "p", Value: ""}, {Key: "c", Value: "text"}}, Mapping{"p", "c", "string"}, "MONGODB_RECORD_INVALID"},
		{"text-limit", bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "p", Value: strings.Repeat("x", int(limits.MaximumTextBytes)+1)}, {Key: "c", Value: "text"}}, Mapping{"p", "c", "string"}, "MONGODB_RECORD_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := bson.Marshal(tc.doc)
			out, e := Convert(raw, tc.mapping, limits)
			if e == nil || e.Error() != tc.code || len(out) != 0 {
				t.Fatalf("unexpected result %s %v", out, e)
			}
		})
	}
}
func TestMongoDBMappingCannotSelectUnapprovedFields(t *testing.T) {
	source := config.MongoDBSource{Fields: []string{"p", "c"}}
	for _, m := range []Mapping{{"secret", "c", "string"}, {"p", "$where", "string"}, {"p", "c", "automatic"}} {
		if m.Validate(source) == nil {
			t.Fatal("unsafe mapping admitted")
		}
	}
	if (Mapping{"p", "c", "string"}).Validate(source) != nil {
		t.Fatal("valid mapping rejected")
	}
}
func TestMongoDBConnectionOptionsPreventURIOverrides(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(root, "uri")
	p := config.DefaultMongoDBConfiguration()
	s := config.MongoDBSource{URIFile: file, Database: "training", TLS: true}
	for _, uri := range []string{"mongodb://fixture:synthetic@example.invalid/training?tlsInsecure=true", "mongodb://fixture:synthetic@example.invalid/?retryReads=true", "mongodb://fixture:synthetic@example.invalid/?authSource=admin&authSource=other", "mongodb://fixture:synthetic@example.invalid/other", "https://example.invalid", "mongodb://fixture:synthetic@example.invalid/?directConnection=FALSE"} {
		if e := os.WriteFile(file, []byte(uri), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := sourceOptions(s, p); e == nil {
			t.Fatalf("unsafe URI admitted: %s", uri)
		}
	}
	if e := os.WriteFile(file, []byte("mongodb://fixture:synthetic@example.invalid/training?authSource=admin"), 0600); e != nil {
		t.Fatal(e)
	}
	o, e := sourceOptions(s, p)
	if e != nil {
		t.Fatal(e)
	}
	if o.RetryReads == nil || *o.RetryReads || o.RetryWrites == nil || *o.RetryWrites || o.TLSConfig == nil || o.TLSConfig.InsecureSkipVerify || *o.MaxPoolSize != 1 {
		t.Fatal("unbounded/insecure driver options")
	}
	s.TLS = false
	o, e = sourceOptions(s, p)
	if e != nil || o.TLSConfig != nil {
		t.Fatal("explicit plaintext not preserved")
	}
	if e := os.Chmod(file, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = sourceOptions(s, p); e == nil {
		t.Fatal("public secret admitted")
	}
}
func TestMongoDBErrorsRedactServerDetails(t *testing.T) {
	for _, e := range []error{mongo.CommandError{Code: 13, Message: "secret-user-and-data"}, errors.New("mongodb://secret@example.invalid"), context.DeadlineExceeded} {
		if strings.Contains(safeError(e).Error(), "secret") {
			t.Fatal("secret in public code")
		}
	}
	encoded, _ := json.Marshal(Result{Data: []byte("private"), Rows: 1, Consistency: "snapshot-session"})
	if strings.Contains(string(encoded), "private") {
		t.Fatal("preflight exposes data")
	}
}

func TestMongoDBOrderedCursorLimitsAndDuplicateRejection(t *testing.T) {
	limits := config.DefaultDatasetPolicy().Limits
	m := Mapping{"p", "c", "string"}
	var out bytes.Buffer
	result := Result{}
	raw := func(id string) bson.Raw {
		oid, e := bson.ObjectIDFromHex(id)
		if e != nil {
			t.Fatal(e)
		}
		b, e := bson.Marshal(bson.D{{Key: "_id", Value: oid}, {Key: "p", Value: "q"}, {Key: "c", Value: "a"}})
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	first := raw("000000000000000000000001")
	second := raw("000000000000000000000002")
	if e := appendDocument(&out, &result, first, m, limits); e != nil {
		t.Fatal(e)
	}
	before := out.String()
	if e := appendDocument(&out, &result, first, m, limits); e == nil || e.Error() != "MONGODB_ORDER_INVALID" || out.String() != before {
		t.Fatal("duplicate modified export")
	}
	limits.MaximumRows = 1
	if e := appendDocument(&out, &result, second, m, limits); e == nil || e.Error() != "MONGODB_LIMIT_EXCEEDED" || out.String() != before {
		t.Fatal("row limit not atomic")
	}
	limits.MaximumRows = 2
	limits.MaximumBytes = int64(out.Len())
	if e := appendDocument(&out, &result, second, m, limits); e == nil || out.String() != before {
		t.Fatal("byte limit not atomic")
	}
	limits = config.DefaultDatasetPolicy().Limits
	if e := appendDocument(&out, &result, second, m, limits); e != nil {
		t.Fatal(e)
	}
	if e := appendDocument(&out, &result, first, m, limits); e == nil || e.Error() != "MONGODB_ORDER_INVALID" {
		t.Fatal("out-of-order source admitted")
	}
	if result.Rows != 2 || result.FirstID != "000000000000000000000001" || result.LastID != "000000000000000000000002" {
		t.Fatal("wrong watermark")
	}
}
