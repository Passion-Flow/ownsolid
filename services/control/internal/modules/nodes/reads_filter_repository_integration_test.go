//go:build repository_integration

package nodes_test

import (
	"crypto/sha256"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func nodeCredential(id string) []byte {
	digest := sha256.Sum256([]byte(id))
	return digest[:]
}

// nodeRow places one node at a known age, so its connectivity is known.
func (x nodeReadActual) nodeRow(t *testing.T, name, status string, age time.Duration) string {
	t.Helper()
	id := uuid.NewString()
	seen := time.Now().UTC().Add(-age)
	// A removed node has no credential; the table insists.
	credential := nodeCredential(id)
	if status == "removed" {
		credential = nil
	}
	x.exec(t,
		"INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'1.0','{\"os_id\":\"synthetic\"}',$7,$7,$7)",
		"INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at,updated_at) VALUES(?,?,?,?,?,?,'1.0','{\"os_id\":\"synthetic\"}',?,?,?)",
		id, x.f.Tenant, x.f.Workspace, name, status, credential, seen)
	return id
}

func TestNodeListFiltersAndCountsAgree(t *testing.T) {
	x := newNodeReadActual(t)
	policy := x.owner.Config.Policy().Nodes
	online := time.Duration(0)
	unknown := time.Duration(policy.OnlineSeconds+policy.OfflineSeconds) * time.Second / 2
	offline := time.Duration(policy.OfflineSeconds+3600) * time.Second

	onlineNode := x.nodeRow(t, "Filter-online", "active", online)
	unknownNode := x.nodeRow(t, "filter-unknown", "active", unknown)
	offlineNode := x.nodeRow(t, "filter-offline", "active", offline)
	disabledNode := x.nodeRow(t, "filter-disabled", "disabled", online)
	removedNode := x.nodeRow(t, "filter-removed", "removed", online)
	percentNode := x.nodeRow(t, "100%_node", "active", online)

	names := func(values url.Values) []string {
		t.Helper()
		page, e := x.s.List(x.ctx, x.admin, "", values)
		if e != nil {
			t.Fatal(values, e)
		}
		out := []string{}
		for _, n := range page.Items {
			out = append(out, n.ID)
		}
		return out
	}
	narrowed := func(extra url.Values) []string {
		t.Helper()
		values := url.Values{"name_prefix": {"filter-"}, "limit": {"100"}}
		for key, value := range extra {
			values[key] = value
		}
		return names(values)
	}
	same := func(label string, got []string, want ...string) {
		t.Helper()
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}

	same("a prefix ignores case and hides removed nodes", narrowed(nil), onlineNode, unknownNode, offlineNode, disabledNode)
	same("online", narrowed(url.Values{"connectivity": {"online"}}), onlineNode, disabledNode)
	same("unknown", narrowed(url.Values{"connectivity": {"unknown"}}), unknownNode)
	same("offline", narrowed(url.Values{"connectivity": {"offline"}}), offlineNode)
	same("active", narrowed(url.Values{"status": {"active"}}), onlineNode, unknownNode, offlineNode)
	same("disabled", narrowed(url.Values{"status": {"disabled"}}), disabledNode)
	same("removed is shown on request", narrowed(url.Values{"status": {"removed"}}), removedNode)
	same("combined", narrowed(url.Values{"connectivity": {"online"}, "status": {"active"}}), onlineNode)
	same("a wildcard in a prefix is plain text", names(url.Values{"name_prefix": {"100%_"}}), percentNode)
	same("a wildcard does not match anything else", names(url.Values{"name_prefix": {"filter%"}}))

	t.Run("a cursor belongs to its filter", func(t *testing.T) {
		values := url.Values{"name_prefix": {"filter-"}, "limit": {"1"}}
		page, e := x.s.List(x.ctx, x.admin, "", values)
		if e != nil || page.NextCursor == nil {
			t.Fatal(page, e)
		}
		values["cursor"] = []string{*page.NextCursor}
		if _, e = x.s.List(x.ctx, x.admin, "", values); e != nil {
			t.Fatal(e)
		}
		values["connectivity"] = []string{"online"}
		if _, e = x.s.List(x.ctx, x.admin, "", values); e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatal(e)
		}
	})

	t.Run("bad filters are refused", func(t *testing.T) {
		for _, values := range []url.Values{
			{"connectivity": {"up"}}, {"status": {"gone"}}, {"name_prefix": {""}}, {"connectivity": {"online", "offline"}}, {"other": {"x"}},
		} {
			if _, e := x.s.List(x.ctx, x.admin, "", values); e == nil || e.Error() != "VALIDATION_FAILED" {
				t.Fatal(values, e)
			}
		}
	})

	t.Run("counts match the lists", func(t *testing.T) {
		counts, e := x.s.Counts(x.ctx, x.admin)
		if e != nil {
			t.Fatal(e)
		}
		all := url.Values{"limit": {"100"}}
		if int64(len(names(all))) != counts.Total {
			t.Fatal(counts, names(all))
		}
		for connectivity, want := range map[string]int64{"online": counts.Online, "unknown": counts.Unknown, "offline": counts.Offline} {
			if got := int64(len(names(url.Values{"limit": {"100"}, "connectivity": {connectivity}}))); got != want {
				t.Fatal(connectivity, got, want)
			}
		}
		if got := int64(len(names(url.Values{"limit": {"100"}, "status": {"disabled"}}))); got != counts.Disabled || got != 1 {
			t.Fatal(got, counts.Disabled)
		}
		if counts.Online+counts.Unknown+counts.Offline != counts.Total {
			t.Fatal(counts)
		}
	})

	t.Run("the portal cannot count or list the fleet", func(t *testing.T) {
		if _, e := x.s.Counts(x.ctx, x.portal); e == nil || e.Error() != "ACCESS_DENIED" {
			t.Fatal(e)
		}
		if _, e := x.s.List(x.ctx, x.portal, "", nil); e == nil || e.Error() != "ACCESS_DENIED" {
			t.Fatal(e)
		}
	})

	t.Run("a workspace member sees the filter within the workspace", func(t *testing.T) {
		page, e := x.s.List(x.ctx, x.portal, x.f.Workspace, url.Values{"name_prefix": {"filter-"}, "connectivity": {"offline"}})
		if e != nil || len(page.Items) != 1 || page.Items[0].ID != offlineNode {
			t.Fatal(page, e)
		}
	})

	t.Run("another tenant's nodes do not count", func(t *testing.T) {
		before, e := x.s.Counts(x.ctx, x.admin)
		if e != nil {
			t.Fatal(e)
		}
		other, workspace, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
		x.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,$2,'active')", "INSERT INTO tenants(id,name,status) VALUES(?,?,'active')", other, "Other tenant")
		x.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other','active')", workspace, other)
		seen := time.Now().UTC()
		x.exec(t,
			"INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at,updated_at) VALUES($1,$2,$3,'filter-foreign','active',$4,'1.0','{\"os_id\":\"synthetic\"}',$5,$5,$5)",
			"INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at,updated_at) VALUES(?,?,?,'filter-foreign','active',?,'1.0','{\"os_id\":\"synthetic\"}',?,?,?)",
			foreign, other, workspace, nodeCredential(foreign), seen)
		if slices.Contains(narrowed(nil), foreign) {
			t.Fatal("listed another tenant's node")
		}
		after, e := x.s.Counts(x.ctx, x.admin)
		if e != nil || after != before {
			t.Fatal(before, after, e)
		}
	})
}

func TestJoinTokenListShowsPendingOnRequest(t *testing.T) {
	x := newNodeReadActual(t)
	pending, e := x.s.Mint(x.ctx, x.admin, x.f.Workspace, "waiting", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	revoked, e := x.s.Mint(x.ctx, x.admin, x.f.Workspace, "revoked", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = x.s.RevokeToken(x.ctx, x.admin, revoked.ID, "no longer needed", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	page, e := x.s.Tokens(x.ctx, x.admin, url.Values{"status": {"pending"}, "limit": {"100"}})
	if e != nil {
		t.Fatal(e)
	}
	found := map[string]bool{}
	for _, token := range page.Items {
		found[token.ID] = true
		if token.Status != "pending" {
			t.Fatal(token)
		}
	}
	if !found[pending.ID] || found[revoked.ID] {
		t.Fatal(page)
	}
	if _, e = x.s.Tokens(x.ctx, x.admin, url.Values{"status": {"revoked"}}); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal(e)
	}
}
