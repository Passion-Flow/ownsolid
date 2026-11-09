//go:build gateway_integration

package distribution_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/google/uuid"
)

func TestRealGoRustPostgresApplicationBudget(t *testing.T) {
	now := time.Now().UTC()
	if now.Month() != now.Add(2*time.Minute).Month() {
		t.Skip("native application budget fixture requires a stable monthly period")
	}
	realGatewayChain(t, applicationBudget)
}

type applicationQuotaFixture struct {
	sibling, independent distribution.MintedKey
	policy               map[string]any
}

func newApplicationQuotaFixture(t *testing.T, c scenario, a access) *applicationQuotaFixture {
	t.Helper()
	mint := func() distribution.MintedKey {
		t.Helper()
		key, err := c.s.MintKey(t.Context(), a.owner, keyInput(a.alias), uuid.NewString(), uuid.NewString())
		if err != nil || key.Secret == nil {
			t.Fatal("issue workspace quota fixture key", err)
		}
		return key
	}
	sibling, independent := mint(), mint()
	workspace := a.alias.WorkspaceID
	key := func(key distribution.MintedKey) map[string]any {
		return map[string]any{"id": key.Key.ID, "workspace_id": workspace}
	}
	return &applicationQuotaFixture{
		sibling: sibling, independent: independent,
		policy: map[string]any{"tenants": []any{map[string]any{
			"id": c.a.Me.Workspace.TenantID, "workspaces": []string{workspace},
			"limits": map[string]any{"tokens": map[string]int{"monthly": 96}},
			"applications": []any{
				map[string]any{"id": uuid.NewString(), "keys": []any{key(a.key), key(sibling)},
					"limits": map[string]any{"tokens": map[string]int{"monthly": 31}}},
				map[string]any{"id": uuid.NewString(), "keys": []any{key(independent)},
					"limits": map[string]any{"tokens": map[string]int{"monthly": 31}}},
			},
		}}},
	}
}

func (q *applicationQuotaFixture) verify(t *testing.T, c scenario, a access, request func(string, string, any) *http.Response, generations *atomic.Int32) {
	t.Helper()
	body := map[string]any{"model": a.alias.Name, "max_tokens": 16,
		"messages": []any{map[string]any{"role": "user", "content": "Synthetic application budget request"}}}
	for _, probe := range []struct {
		key    distribution.MintedKey
		status int
	}{
		{a.key, http.StatusOK}, {q.sibling, http.StatusTooManyRequests}, {q.independent, http.StatusOK},
	} {
		answer := request("/v1/chat/completions", *probe.key.Secret, body)
		id := answer.Header.Get("X-Request-ID")
		var result struct {
			Usage *wire.Usage
			Error struct{ Code, Message string }
		}
		err := json.NewDecoder(answer.Body).Decode(&result)
		answer.Body.Close()
		if err != nil || answer.StatusCode != probe.status || !wire.ValidID(id) {
			t.Fatal("application budget response", probe.status, answer.StatusCode, err)
		}
		if probe.status == http.StatusTooManyRequests {
			retry, err := strconv.Atoi(answer.Header.Get("Retry-After"))
			if err != nil || retry < 1 || result.Error.Code != "monthly_token_budget_exceeded" ||
				!strings.Contains(result.Error.Message, "application monthly token budget") {
				t.Fatal("expected the shared application budget refusal", result.Error.Code, err)
			}
		} else if result.Usage == nil || result.Usage.TotalTokens != 9 {
			t.Fatal("successful quota fixture request did not settle nine tokens")
		}
		verifyApplicationBudgetReceipt(t, c, a.alias.WorkspaceID, id, probe.key.Key.ID, probe.status)
	}
	if generations.Load() != 2 {
		t.Fatal("application budget refusal reached generation", generations.Load())
	}
}

func verifyApplicationBudgetReceipt(t *testing.T, c scenario, workspace, id, key string, status int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := c.s.Request(ctx, c.a, workspace, id)
		if err != nil || record.APIKeyID != key {
			t.Fatal("quota receipt authority", err)
		}
		if record.Outcome != nil {
			if record.CapacityHeld {
				t.Fatal("quota receipt did not release deployment capacity")
			}
			if status == http.StatusTooManyRequests {
				if record.Outcome.Status != "upstream_error" || record.Outcome.Usage != nil || record.Outcome.OutputStarted {
					t.Fatal("quota refusal persisted generation or usage")
				}
			} else if record.Outcome.Status != "succeeded" || record.Outcome.Usage == nil || record.Outcome.Usage.TotalTokens != 9 {
				t.Fatal("successful quota receipt lost settled usage")
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("quota outcome was not reported")
		case <-ticker.C:
		}
	}
}
