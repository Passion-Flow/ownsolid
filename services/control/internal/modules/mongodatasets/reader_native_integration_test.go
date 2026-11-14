//go:build mongodb_integration

package mongodatasets

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/mongodbfixture"
)

// Runs the real reader and actual TLS/SCRAM replica set. Command monitoring only
// schedules mutations/faults at a proven cursor boundary; it returns no fake data.
func TestMongoDBNativeSnapshotMutationAndGetMoreFailures(t *testing.T) {
	f := mongodbfixture.New(t)
	source := f.Source
	p := config.DefaultMongoDBConfiguration()
	p.Enabled, p.BatchSize = true, 2
	mapping := Mapping{Prompt: "question", Completion: "answer", Conversion: "string"}
	limits := config.DefaultDatasetPolicy().Limits
	original, err := Read(context.Background(), source, p, mapping, limits, false)
	if err != nil || original.Rows != 8 {
		t.Fatal("initial real source", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	collection := f.Admin.Database(source.Database).Collection(source.Collection)
	opts, err := sourceOptions(source, p)
	if err != nil {
		t.Fatal(err)
	}
	mutations, batches := 0, 0
	opts.SetMonitor(&event.CommandMonitor{Succeeded: func(c context.Context, e *event.CommandSucceededEvent) {
		if e.CommandName == "getMore" {
			batches++
		}
		if e.CommandName != "find" {
			return
		}
		mutations++
		if _, err := collection.UpdateMany(ctx, bson.D{}, bson.D{{Key: "$set", Value: bson.D{{Key: "answer", Value: "changed after first batch"}}}}); err != nil {
			t.Error(err)
		}
		if _, err := collection.DeleteOne(ctx, bson.D{{Key: "_id", Value: mustID(t, original.LastID)}}); err != nil {
			t.Error(err)
		}
		if _, err := collection.InsertOne(ctx, bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "question", Value: "new after first batch"}, {Key: "answer", Value: "new"}}); err != nil {
			t.Error(err)
		}
	}})
	snapshot, err := readOptions(ctx, source, p, mapping, limits, false, opts)
	if err != nil || mutations != 1 || batches < 3 || !bytes.Equal(snapshot.Data, original.Data) {
		t.Fatalf("one snapshot across mutation: rows=%d mutations=%d getMore=%d error=%v", snapshot.Rows, mutations, batches, err)
	}
	fresh, err := Read(ctx, source, p, mapping, limits, false)
	if err != nil || bytes.Equal(fresh.Data, original.Data) || !strings.Contains(string(fresh.Data), "changed after first batch") {
		t.Fatal("fresh session did not observe actual committed mutation", err)
	}
	// Fault injection is enabled only in this owned test server. The production
	// source principal cannot issue configureFailPoint or any write command.
	failpoint := func(data bson.D) {
		t.Helper()
		if err := f.Admin.Database("admin").RunCommand(ctx, bson.D{{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: bson.D{{Key: "times", Value: 1}}}, {Key: "data", Value: data}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		data    bson.D
		timeout int
		code    string
	}{
		{"disconnect", bson.D{{Key: "failCommands", Value: bson.A{"getMore"}}, {Key: "closeConnection", Value: true}}, 15, "MONGODB_READ_FAILED"},
		{"deadline", bson.D{{Key: "failCommands", Value: bson.A{"getMore"}}, {Key: "blockConnection", Value: true}, {Key: "blockTimeMS", Value: 3000}}, 1, "MONGODB_TIMEOUT"},
		{"snapshot_expired", bson.D{{Key: "failCommands", Value: bson.A{"getMore"}}, {Key: "errorCode", Value: 286}}, 15, "MONGODB_SNAPSHOT_EXPIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failpoint(tc.data)
			policy := p
			policy.TimeoutSeconds = tc.timeout
			got, err := Read(ctx, source, policy, mapping, limits, false)
			if err == nil || err.Error() != tc.code || got.Rows != 0 || len(got.Data) != 0 {
				t.Fatalf("partial snapshot escaped: rows=%d error=%v", got.Rows, err)
			}
			// No cross-snapshot append: the next explicit read starts from its first ID.
			recovered, err := Read(ctx, source, p, mapping, limits, false)
			if err != nil || !bytes.Equal(recovered.Data, fresh.Data) {
				t.Fatal("fresh full read after discard", err)
			}
		})
	}
}
func mustID(t *testing.T, hex string) bson.ObjectID {
	t.Helper()
	id, err := bson.ObjectIDFromHex(hex)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
