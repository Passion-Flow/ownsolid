//go:build repository_integration

package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/google/uuid"
)

func TestTemporaryPortalWorkspaceScopeFixture(t *testing.T) {
	root := "/fixture"
	x := serverCompositionSetup(t, false)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	x.ctx = ctx
	x.c.Server.ListenAddress = "0.0.0.0:14850"
	x.c.Server.AdminOrigin = "https://admin.example.invalid:14850"
	x.c.Server.PortalOrigin = "https://portal.example.invalid:14850"
	x.c.Server.PortalAssets = "/web-portal"
	x.configureTLS(t)
	x.owner.Config, x.mail.Config = x.c, x.c

	second, foreignTenant, foreignScope := uuid.NewString(), uuid.NewString(), uuid.NewString()
	x.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')", "", foreignTenant)
	x.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Second member workspace','active'),($3,$4,'Synthetic foreign scope','active')", "", second, x.f.Tenant, foreignScope, foreignTenant)
	type scope struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Application string `json:"application"`
	}
	firstScope := scope{x.f.Workspace, "Repository Workspace", "First workspace application"}
	nextScope := scope{second, "Second member workspace", "Second workspace application"}
	marker := "Foreign-tenant-application-" + uuid.NewString()
	people := make([]map[string]any, 0, 2)
	members := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		id := uuid.NewString()
		members = append(members, id)
		email := fmt.Sprintf("scope-member-%d@example.invalid", i)
		password := "Fixture-Portal-Scope-" + uuid.NewString()
		hash, err := x.owner.Passwords.Hash(ctx, password)
		if err != nil {
			t.Fatal(err)
		}
		x.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,email_verified_at) VALUES($1,$2,$2,$3,'en','active',clock_timestamp())", "", id, email, fmt.Sprintf("Scope member %d", i))
		x.exec(t, "INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "", id, hash)
		x.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "", id, x.f.Tenant, second)
		current := nextScope
		if i == 0 {
			current = firstScope
			x.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "", id, x.f.Tenant, x.f.Workspace)
		}
		people = append(people, map[string]any{"email": email, "password": password, "workspace": current})
	}
	foreignApplication := ""
	graph := []byte(`{"schema_version":1,"nodes":[],"edges":[]}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(graph))
	for _, item := range []struct{ tenant, workspace, name string }{
		{x.f.Tenant, x.f.Workspace, firstScope.Application},
		{x.f.Tenant, second, nextScope.Application},
		{foreignTenant, foreignScope, marker},
	} {
		workflow, version := uuid.NewString(), uuid.NewString()
		if item.tenant == foreignTenant {
			foreignApplication = workflow
		}
		x.exec(t, `INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,enabled,execution_authority,draft_graph,draft_sha256,created_by)
            VALUES($1,$2,$3,$4::text,lower($4::text),true,$5,$6::jsonb,$7,$8)`, "", workflow, item.tenant, item.workspace, item.name, uuid.NewString(), graph, digest, x.f.User)
		x.exec(t, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by)
            SELECT $1,tenant_id,workspace_id,id,1,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=$2`, "", version, workflow)
		x.exec(t, "UPDATE workflows SET current_version_id=$1 WHERE id=$2", "", version, workflow)
	}

	handler := identityhttp.New(x.owner)
	if err := handler.InitializationError(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", x.c.Server.ListenAddress)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	ended := make(chan error, 1)
	go func() {
		ended <- server.ServeTLS(listener, x.c.Server.TLSCertificateFile, x.c.Server.TLSPrivateKeyFile)
	}()
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(stop); err != nil {
			t.Error(err)
		}
		if err := <-ended; err != http.ErrServerClosed {
			t.Error(err)
		}
	})
	cert, err := os.ReadFile(x.c.Server.TLSCertificateFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(cert)
	if block == nil {
		t.Fatal("browser certificate is absent")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	data, err := json.Marshal(map[string]any{
		"people": people, "second": nextScope,
		"foreign": map[string]string{"workspace": foreignScope, "marker": marker},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "person.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spki"), []byte(base64.StdEncoding.EncodeToString(pin[:])), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("PORTAL_SCOPE_BROWSER_READY")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("browser did not finish the scope fixture")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(root, "browser-finished")); err != nil {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, "scope-proof.json"))
			if err != nil {
				t.Fatal("browser proof is absent", err)
			}
			var proof struct {
				Refused []string `json:"refused_request_ids"`
			}
			if err := json.Unmarshal(data, &proof); err != nil {
				t.Fatal("invalid browser correlation proof", err)
			}
			if len(proof.Refused) != 2 {
				t.Fatal("two scoped refusals required")
			}
			for _, id := range proof.Refused {
				if err := uuid.Validate(id); err != nil {
					t.Fatal(err)
				}
			}
			var canary, memberships, successful int
			if err := x.f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM workflows w JOIN workflow_versions v ON v.id=w.current_version_id AND v.workflow_id=w.id
                WHERE w.id=$1 AND w.tenant_id=$2 AND w.workspace_id=$3 AND w.name=$4 AND w.enabled AND v.version_number=1`,
				foreignApplication, foreignTenant, foreignScope, marker).Scan(&canary); err != nil || canary != 1 {
				t.Fatal("foreign projection changed", canary, err)
			}
			if err := x.f.PostgreSQL.QueryRow(ctx, "SELECT count(*) FROM workspace_memberships WHERE user_id=ANY($1::uuid[]) AND (tenant_id=$2 OR workspace_id=$3)", members, foreignTenant, foreignScope).Scan(&memberships); err != nil || memberships != 0 {
				t.Fatal("foreign access unexpectedly granted", memberships, err)
			}
			if err := x.f.PostgreSQL.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE outcome IN ('success','accepted') AND (request_id=ANY($1::uuid[]) OR (actor_user_id=ANY($2::uuid[]) AND (tenant_id=$3 OR workspace_id=$4)))", proof.Refused, members, foreignTenant, foreignScope).Scan(&successful); err != nil || successful != 0 {
				t.Fatal("refused request recorded success", successful, err)
			}
			fmt.Printf("PORTAL_SCOPE_DB_PROOF foreign_published_projection=%d foreign_memberships=%d refused_success_audits=%d request_ids=%v\n", canary, memberships, successful, proof.Refused)
			return
		}
	}
}
