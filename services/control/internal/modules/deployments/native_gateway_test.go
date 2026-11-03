package deployments

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"reflect"
	"testing"
	"time"
)

type nativeGatewayProbe struct{ transaction.Executor }

func (nativeGatewayProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func TestNativeGatewayFenceTimeIsExact(t *testing.T) {
	stamp := time.Date(2026, 10, 24, 1, 2, 3, 123456000, time.UTC)
	if !gatewayNativeTime(stamp) {
		t.Fatal("microsecond UTC timestamp rejected")
	}
	for _, invalid := range []time.Time{time.Time{}, stamp.Add(time.Nanosecond), stamp.In(time.FixedZone("offset", 3600)), time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if gatewayNativeTime(invalid) {
			t.Fatal("receipt time must not be rounded or reinterpreted")
		}
		if !reflect.DeepEqual(gatewayNativeSQL(nativeGatewayProbe{}, "native", "pg", "mysql", invalid), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("invalid native timestamp reached statement")
		}
	}
	if !reflect.DeepEqual(gatewaySQL(nativeGatewayProbe{}, "pg", "mysql"), transaction.SQL(transaction.Dameng, "")) {
		t.Fatal("unimplemented native query fell through")
	}
}
