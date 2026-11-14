package directory

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	ldap "github.com/go-ldap/ldap/v3"
)

var (
	errConfiguration = errors.New("DIRECTORY_CONFIGURATION_INVALID")
	errCredentials   = errors.New("DIRECTORY_CREDENTIALS_REFUSED")
	errUnavailable   = errors.New("DIRECTORY_UNAVAILABLE_RETRY")
	errIncomplete    = errors.New("DIRECTORY_SCAN_INCOMPLETE_RETRY")
)

type config struct {
	Origin, DialAddress, CAFile, SearchDN, SearchPasswordFile, UserBase, GroupBase    string
	Timeout                                                                           time.Duration
	FrameBytes, TotalBytes, Depth, Nodes, PageSize, MaxPages, MaxEntries, CookieBytes int
}
type client struct {
	cfg   config
	roots *x509.CertPool
	name  string
	dials atomic.Int64
}
type entry struct {
	DN, ID  string
	Members []string
}
type connection struct {
	raw     net.Conn
	sdk     *ldap.Conn
	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func newClient(c config) (*client, error) {
	u, e := url.Parse(c.Origin)
	if e != nil || u.Scheme != "ldaps" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" || u.Port() == "" {
		return nil, errConfiguration
	}
	host, port, e := net.SplitHostPort(c.DialAddress)
	portNumber, portError := strconv.Atoi(port)
	if e != nil || portError != nil || portNumber < 1 || portNumber > 65535 || net.ParseIP(host) == nil || port != u.Port() || c.Timeout <= 0 || c.Timeout > time.Minute || c.FrameBytes < 256 || c.FrameBytes > 1<<20 || c.TotalBytes < c.FrameBytes || c.TotalBytes > 16<<20 || c.Depth < 4 || c.Depth > 32 || c.Nodes < 16 || c.Nodes > 4096 || c.PageSize < 1 || c.PageSize > 100 || c.MaxPages < 1 || c.MaxPages > 100 || c.MaxEntries < 1 || c.MaxEntries > 10000 || c.CookieBytes < 1 || c.CookieBytes > 4096 {
		return nil, errConfiguration
	}
	for _, dn := range []string{c.SearchDN, c.UserBase, c.GroupBase} {
		if dn == "" {
			return nil, errConfiguration
		}
		if _, e := ldap.ParseDN(dn); e != nil {
			return nil, errConfiguration
		}
	}
	caInfo, e := os.Stat(c.CAFile)
	if e != nil || !caInfo.Mode().IsRegular() || caInfo.Size() < 1 || caInfo.Size() > 1<<20 {
		return nil, errConfiguration
	}
	ca, e := os.ReadFile(c.CAFile)
	if e != nil || len(ca) > 1<<20 {
		return nil, errConfiguration
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errConfiguration
	}
	if _, e := secret(c.SearchPasswordFile); e != nil {
		return nil, errConfiguration
	}
	return &client{cfg: c, roots: roots, name: u.Hostname()}, nil
}

func secret(path string) (string, error) {
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() < 1 || st.Size() > 16384 {
		return "", errConfiguration
	}
	f, e := os.Open(path)
	if e != nil {
		return "", errConfiguration
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(st, opened) {
		return "", errConfiguration
	}
	current, e := os.Lstat(path)
	if e != nil || !current.Mode().IsRegular() || !os.SameFile(st, current) {
		return "", errConfiguration
	}
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(b) < 1 || len(b) > 16384 || !utf8.Valid(b) || strings.ContainsAny(string(b), "\x00\r\n") {
		return "", errConfiguration
	}
	return string(b), nil
}

func (c *client) open(ctx context.Context) (*connection, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errConfiguration
	}
	c.dials.Add(1)
	raw, e := (&net.Dialer{}).DialContext(ctx, "tcp", c.cfg.DialAddress)
	if e != nil {
		return nil, errUnavailable
	}
	if e = raw.SetDeadline(deadline); e != nil {
		raw.Close()
		return nil, errUnavailable
	}
	t := tls.Client(raw, &tls.Config{RootCAs: c.roots, ServerName: c.name, MinVersion: tls.VersionTLS12})
	if e = t.HandshakeContext(ctx); e != nil {
		raw.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errUnavailable
	}
	g := &frameConn{Conn: t, limits: bounds{c.cfg.FrameBytes, c.cfg.TotalBytes, c.cfg.Depth, c.cfg.Nodes}}
	sdk := ldap.NewConn(g, true)
	sdk.SetTimeout(time.Until(deadline))
	sdk.Start()
	x := &connection{raw: raw, sdk: sdk, stop: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(x.stopped)
		select {
		case <-ctx.Done():
			raw.Close()
		case <-x.stop:
		}
	}()
	return x, nil
}
func (x *connection) close() {
	x.once.Do(func() { close(x.stop); x.raw.Close(); x.sdk.Close(); <-x.stopped })
}
func operationError(ctx context.Context, fallback error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	return fallback
}
func (c *client) searchConnection(ctx context.Context) (*connection, error) {
	pw, e := secret(c.cfg.SearchPasswordFile)
	if e != nil {
		return nil, e
	}
	x, e := c.open(ctx)
	if e != nil {
		return nil, e
	}
	if e = x.sdk.Bind(c.cfg.SearchDN, pw); e != nil {
		x.close()
		return nil, operationError(ctx, errUnavailable)
	}
	return x, nil
}

func one(e *ldap.Entry, name string) (string, error) {
	seen := false
	v := ""
	for _, a := range e.Attributes {
		if strings.EqualFold(a.Name, name) {
			if seen || len(a.ByteValues) != 1 {
				return "", errIncomplete
			}
			seen = true
			v = string(a.ByteValues[0])
		}
	}
	if !seen || v == "" {
		return "", errIncomplete
	}
	return v, nil
}
func stable(e *ldap.Entry) (string, error) {
	v, err := one(e, "entryUUID")
	if err != nil || len(v) != 36 || v[8] != '-' || v[13] != '-' || v[18] != '-' || v[23] != '-' || strings.ToLower(v) != v {
		return "", errIncomplete
	}
	b, err := hex.DecodeString(strings.ReplaceAll(v, "-", ""))
	if err != nil || len(b) != 16 {
		return "", errIncomplete
	}
	nonzero := false
	for _, x := range b {
		nonzero = nonzero || x != 0
	}
	if !nonzero {
		return "", errIncomplete
	}
	return v, nil
}

func (c *client) login(ctx context.Context, login, password string) (entry, error) {
	if login == "" || len(login) > 256 || !utf8.ValidString(login) || strings.ContainsRune(login, 0) || password == "" || len(password) > 16384 || !utf8.ValidString(password) || strings.ContainsRune(password, 0) {
		return entry{}, errCredentials
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	x, e := c.searchConnection(ctx)
	if e != nil {
		return entry{}, e
	}
	q := ldap.NewSearchRequest(c.cfg.UserBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 0, false, "(&(objectClass=inetOrgPerson)(uid="+ldap.EscapeFilter(login)+"))", []string{"entryUUID"}, nil)
	q.EnforceSizeLimit = true
	r, e := x.sdk.Search(q)
	x.close()
	if e != nil || r == nil || len(r.Referrals) > 0 || len(r.Controls) > 0 {
		return entry{}, operationError(ctx, errUnavailable)
	}
	if len(r.Entries) != 1 {
		return entry{}, errCredentials
	}
	found := r.Entries[0]
	id, e := stable(found)
	if e != nil {
		return entry{}, errCredentials
	}
	dn, e := ldap.ParseDN(found.DN)
	base, _ := ldap.ParseDN(c.cfg.UserBase)
	if e != nil || !base.AncestorOfFold(dn) {
		return entry{}, errCredentials
	}
	user, e := c.open(ctx)
	if e != nil {
		return entry{}, e
	}
	defer user.close()
	if e = user.sdk.Bind(found.DN, password); e != nil {
		refusal := errUnavailable
		if ldap.IsErrorWithCode(e, ldap.LDAPResultInvalidCredentials) {
			refusal = errCredentials
		}
		return entry{}, operationError(ctx, refusal)
	}
	verify := ldap.NewSearchRequest(found.DN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 0, false, "(objectClass=*)", []string{"entryUUID"}, nil)
	verify.EnforceSizeLimit = true
	current, e := user.sdk.Search(verify)
	if e != nil || current == nil || len(current.Referrals) > 0 || len(current.Controls) > 0 || len(current.Entries) != 1 {
		return entry{}, operationError(ctx, errUnavailable)
	}
	currentID, e := stable(current.Entries[0])
	if e != nil || currentID != id {
		return entry{}, errCredentials
	}
	if e = ctx.Err(); e != nil {
		return entry{}, e
	}
	// A successful bind is only an external identity proof. No local session or
	// role is created by this protocol prototype.
	return entry{DN: found.DN, ID: id}, nil
}

func (c *client) groups(ctx context.Context) ([]entry, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	x, e := c.searchConnection(ctx)
	if e != nil {
		return nil, e
	}
	defer x.close()
	var all []entry
	var cookie []byte
	seenCookies := map[string]bool{}
	seenIDs := map[string]bool{}
	for page := 0; page < c.cfg.MaxPages; page++ {
		control := ldap.NewControlPaging(uint32(c.cfg.PageSize))
		control.SetCookie(cookie)
		q := ldap.NewSearchRequest(c.cfg.GroupBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, c.cfg.MaxEntries, 0, false, "(objectClass=groupOfNames)", []string{"entryUUID", "member"}, []ldap.Control{control})
		q.EnforceSizeLimit = true
		r, e := x.sdk.Search(q)
		if e != nil || r == nil || len(r.Referrals) > 0 || len(r.Entries) > c.cfg.PageSize || len(r.Controls) != 1 {
			return nil, operationError(ctx, errIncomplete)
		}
		next, ok := r.Controls[0].(*ldap.ControlPaging)
		if !ok || len(next.Cookie) > c.cfg.CookieBytes {
			return nil, errIncomplete
		}
		for _, v := range r.Entries {
			id, e := stable(v)
			if e != nil || seenIDs[id] || len(all) >= c.cfg.MaxEntries {
				return nil, errIncomplete
			}
			dn, e := ldap.ParseDN(v.DN)
			base, _ := ldap.ParseDN(c.cfg.GroupBase)
			if e != nil || !base.AncestorOfFold(dn) {
				return nil, errIncomplete
			}
			// This first OpenLDAP protocol slice reads bounded membership values only;
			// AD ranged attributes and reference reconciliation are not implemented.
			var members []string
			for _, a := range v.Attributes {
				if strings.EqualFold(a.Name, "member") {
					if members != nil || len(a.Values) > c.cfg.MaxEntries {
						return nil, errIncomplete
					}
					members = append([]string{}, a.Values...)
				} else if strings.HasPrefix(strings.ToLower(a.Name), "member;") {
					return nil, errIncomplete
				}
			}
			if len(members) == 0 {
				return nil, errIncomplete
			}
			seenIDs[id] = true
			all = append(all, entry{DN: v.DN, ID: id, Members: members})
		}
		if len(next.Cookie) == 0 {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			return all, nil
		}
		if len(r.Entries) == 0 || seenCookies[string(next.Cookie)] {
			return nil, errIncomplete
		}
		seenCookies[string(next.Cookie)] = true
		cookie = append([]byte(nil), next.Cookie...)
	}
	return nil, errIncomplete
}
