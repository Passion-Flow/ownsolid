package directory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	ldap "github.com/go-ldap/ldap/v3"
)

type liveFixture struct {
	Config                                       config
	UserPasswordFile, AdminDN, AdminPasswordFile string
}

func live(t *testing.T) (*client, liveFixture) {
	t.Helper()
	path := os.Getenv("IAM103_FIXTURE_CONFIG")
	if path == "" {
		t.Skip("private OpenLDAP fixture not selected")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var f liveFixture
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	c, e := newClient(f.Config)
	if e != nil {
		t.Fatal(e)
	}
	return c, f
}
func TestLiveLDAPSAuthentication(t *testing.T) {
	c, f := live(t)
	password := stringSecret(t, f.UserPasswordFile)
	got, e := c.login(context.Background(), "alice", password)
	if e != nil || got.ID == "" {
		t.Fatal("verified directory login failed", e)
	}
	if c.dials.Load() != 2 {
		t.Fatal("search and user bind did not use separate connections")
	}
	if got, e = c.login(context.Background(), "alice", stringSecret(t, f.AdminPasswordFile)); !errors.Is(e, errCredentials) || got.ID != "" {
		t.Fatal("wrong password accepted", e)
	}
	for _, name := range []string{"*)(uid=*)", "duplicate", "missing"} {
		if got, e = c.login(context.Background(), name, password); !errors.Is(e, errCredentials) || got.ID != "" {
			t.Fatal("nonunique/escaped user accepted", e)
		}
	}
	n := c.dials.Load()
	if _, e = c.login(context.Background(), "alice", ""); !errors.Is(e, errCredentials) {
		t.Fatal(e)
	}
	if c.dials.Load() != n {
		t.Fatal("empty password opened socket")
	}
}
func TestLiveDirectoryReaderCannotWrite(t *testing.T) {
	c, _ := live(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	x, e := c.searchConnection(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer x.close()
	q := ldap.NewModifyRequest("uid=alice,"+c.cfg.UserBase, nil)
	q.Replace("description", []string{"refused fixture write"})
	if e = x.sdk.Modify(q); !ldap.IsErrorWithCode(e, ldap.LDAPResultInsufficientAccessRights) {
		t.Fatal("search identity was not refused for write")
	}
}
func TestLiveDirectoryStableIdentity(t *testing.T) {
	c, f := live(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, e := c.open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.close()
	if e = admin.sdk.Bind(f.AdminDN, stringSecret(t, f.AdminPasswordFile)); e != nil {
		t.Fatal("fixture admin bind failed")
	}
	pw := stringSecret(t, f.UserPasswordFile)
	oldDN := "uid=rename," + c.cfg.UserBase
	newDN := "uid=renamed," + c.cfg.UserBase
	add := func(dn, uid string) {
		q := ldap.NewAddRequest(dn, nil)
		q.Attribute("objectClass", []string{"inetOrgPerson"})
		q.Attribute("uid", []string{uid})
		q.Attribute("cn", []string{"Synthetic rename"})
		q.Attribute("sn", []string{"Fixture"})
		q.Attribute("userPassword", []string{pw})
		if e := admin.sdk.Add(q); e != nil {
			t.Fatal("fixture add failed")
		}
	}
	add(oldDN, "rename")
	defer func() { admin.sdk.Del(ldap.NewDelRequest(oldDN, nil)); admin.sdk.Del(ldap.NewDelRequest(newDN, nil)) }()
	first, e := c.login(ctx, "rename", pw)
	if e != nil {
		t.Fatal(e)
	}
	if e = admin.sdk.ModifyDN(ldap.NewModifyDNRequest(oldDN, "uid=renamed", true, "")); e != nil {
		t.Fatal("fixture rename failed")
	}
	renamed, e := c.login(ctx, "renamed", pw)
	if e != nil || first.ID != renamed.ID || first.DN == renamed.DN {
		t.Fatal("rename changed stable identity", e)
	}
	if e = admin.sdk.Del(ldap.NewDelRequest(newDN, nil)); e != nil {
		t.Fatal("fixture delete failed")
	}
	add(newDN, "renamed")
	recreated, e := c.login(ctx, "renamed", pw)
	if e != nil || recreated.ID == first.ID {
		t.Fatal("recreated name inherited old identity", e)
	}
}
func TestLiveDirectoryPagingFailureAndRetry(t *testing.T) {
	c, f := live(t)
	all, e := c.groups(context.Background())
	if e != nil || len(all) != 5 {
		t.Fatal("complete scan failed", len(all), e)
	}
	for _, v := range all {
		if len(v.Members) != 1 {
			t.Fatal("membership missing")
		}
	}
	for _, change := range []func(*config){func(c *config) { c.MaxPages = 1 }, func(c *config) { c.MaxEntries = 3 }, func(c *config) { c.TotalBytes = c.FrameBytes; c.FrameBytes = 256; c.TotalBytes = 256 }} {
		cfg := f.Config
		change(&cfg)
		bounded, e := newClient(cfg)
		if e != nil {
			t.Fatal(e)
		}
		got, e := bounded.groups(context.Background())
		if e == nil || got != nil {
			t.Fatal("partial scan exposed as complete")
		}
	}
	retry, e := c.groups(context.Background())
	if e != nil || len(retry) != 5 {
		t.Fatal("explicit retry failed", e)
	}
}
