package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Synthetic streaming rows test the actual heap/scorer in isolation. They do
// not establish SQL scope, encoder quality, native or serving qualification.
type streamingVectorRows struct {
	size, index int
	vector      []byte
	invalidAt   int
	cancelAt    int
	cancel      context.CancelFunc
	closed      bool
}

func (r *streamingVectorRows) Next() bool {
	r.index++
	if r.cancel != nil && r.index == r.cancelAt {
		r.cancel()
	}
	return r.index <= r.size
}
func (r *streamingVectorRows) Err() error { return nil }
func (r *streamingVectorRows) Close()     { r.closed = true }
func (r *streamingVectorRows) Scan(dest ...any) error {
	if len(dest) != 10 {
		return errors.New("synthetic row shape")
	}
	raw := r.vector
	if r.index == r.invalidAt {
		raw = []byte(`[0]`)
	}
	values := []any{fmt.Sprintf("00000000-0000-0000-0000-%012d", r.size-r.index+1), "source", int64(1), "synthetic.txt", strings.Repeat("a", 64), 0, 3, 0, []byte("abc"), raw}
	for i, value := range values {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}

type streamingVectorExecutor struct{ rows *streamingVectorRows }

func (streamingVectorExecutor) Dialect() transaction.Dialect { return transaction.MySQL }
func (q streamingVectorExecutor) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	return q.rows, nil
}
func (streamingVectorExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	return 0, errors.New("unexpected synthetic write")
}
func (streamingVectorExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	return syntheticVectorErrorRow{}
}

type syntheticVectorErrorRow struct{}

func (syntheticVectorErrorRow) Scan(...any) error { return errors.New("unexpected synthetic scalar") }
func streamingUnitVector() []float64              { v := make([]float64, EmbeddingDimension); v[0] = 1; return v }
func TestKnowledgeRuntimeStreamingTopKAndRefusal(t *testing.T) {
	for _, name := range []string{"many_ties_small_k", "minimum_score", "cancelled", "invalid_late_vector", "invalid_dimension_and_norm"} {
		t.Run(name, func(t *testing.T) {
			var contract workerContract
			if json.Unmarshal([]byte(WorkerContractJSON), &contract) != nil {
				t.Fatal("contract")
			}
			s := &Service{Identity: &identity.Service{}, Processor: &Processor{contract: contract}}
			raw, _ := json.Marshal(streamingUnitVector())
			rows := &streamingVectorRows{size: 10000, vector: raw, invalidAt: -1}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := SearchRequest{TopK: 3, MinimumScore: 0}
			switch name {
			case "minimum_score":
				v := streamingUnitVector()
				v[0] = -1
				rows.vector, _ = json.Marshal(v)
			case "cancelled":
				rows.cancel, rows.cancelAt = cancel, 101
			case "invalid_late_vector":
				rows.invalidAt = 9999
			case "invalid_dimension_and_norm":
				for _, v := range [][]float64{nil, {1}, make([]float64, EmbeddingDimension)} {
					if _, e := vectorNorm(v, contract.VectorNormTolerance); e == nil {
						t.Fatal("invalid vector accepted")
					}
				}
				for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 2} {
					v := streamingUnitVector()
					v[0] = value
					if _, e := vectorNorm(v, contract.VectorNormTolerance); e == nil {
						t.Fatal("nonfinite/nonunit accepted")
					}
				}
				corrupted := strings.Replace(string(raw), ",0", ",null", 1)
				if _, _, e := decodeVector([]byte(corrupted), contract.VectorNormTolerance); e == nil {
					t.Fatal("null component accepted as numeric zero")
				}
				if _, _, e := decodeVector([]byte(strings.Repeat(" ", maximumStoredEmbeddingBytes+1)), contract.VectorNormTolerance); e == nil {
					t.Fatal("oversized vector JSON accepted")
				}
				return
			}
			found, e := s.mysqlSearch(ctx, streamingVectorExecutor{rows}, workspaces.Workspace{TenantID: "tenant", ID: "workspace"}, "base", "generation", streamingUnitVector(), request)
			if name == "cancelled" || name == "invalid_late_vector" {
				if e == nil || len(found) != 0 {
					t.Fatal("partial result leaked", e)
				}
			} else if name == "minimum_score" {
				if e != nil || len(found) != 0 {
					t.Fatal("below-threshold result", e)
				}
			} else {
				if e != nil || len(found) != 3 {
					t.Fatal("bounded exact result", e)
				}
				for i, m := range found {
					if m.DocumentID != fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1) || m.CitationID != fmt.Sprintf("S%d", i+1) || m.Score != 1 {
						t.Fatal("arrival-independent tie order", found)
					}
				}
			}
			if !rows.closed {
				t.Fatal("stream not closed")
			}
		})
	}
}
