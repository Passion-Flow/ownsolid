package cloudinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func credentialsFixture(t *testing.T) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "credentials.json")
	raw, _ := json.Marshal(Credentials{AccessKeyID: "AKIAEXAMPLE0000000000", SecretAccessKey: "synthetic-secret-for-local-fixture-only"})
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func providerFixture(t *testing.T, serve http.HandlerFunc) (*Provider, config.AWSConnection) {
	t.Helper()
	p := config.DefaultAWSConfiguration()
	p.RetryMilliseconds = 1
	p.RetryAttempts = 2
	a := config.AWSConnection{Name: "fixture", Partition: "aws", AccountID: "123456789012", Regions: []string{"us-east-1"}, WorkspaceIDs: []string{"11111111-1111-4111-8111-111111111111"}, CredentialsFile: credentialsFixture(t)}
	server := httptest.NewServer(serve)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client := server.Client()
	provider := NewProvider(p)
	provider.client = &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		u := *req.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		copy.URL = &u
		copy.Host = req.URL.Host
		return client.Do(copy)
	})}
	return provider, a
}
func caller(w http.ResponseWriter) {
	fmt.Fprint(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Arn>arn:aws:iam::123456789012:user/synthetic-fixture</Arn><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
}
func page(id, next string) string {
	items := ""
	if id != "" {
		items = `<item><ownerId>123456789012</ownerId><instancesSet><item><instanceId>` + id + `</instanceId><instanceType>t3.micro</instanceType><instanceState><name>running</name></instanceState><placement><availabilityZone>us-east-1a</availabilityZone></placement></item></instancesSet></item>`
	}
	return `<DescribeInstancesResponse><reservationSet>` + items + `</reservationSet><nextToken>` + next + `</nextToken></DescribeInstancesResponse>`
}
func wantCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s; got %v", code, e)
	}
}
func TestAWSSigV4Golden(t *testing.T) {
	body := "Action=DescribeInstances&MaxResults=100&Version=2016-11-15"
	req, _ := http.NewRequest("POST", "https://ec2.us-east-1.amazonaws.com/", strings.NewReader(body))
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	sign(req, body, "us-east-1", "ec2", Credentials{AccessKeyID: "AKIAEXAMPLE0000000000", SecretAccessKey: "synthetic-secret-for-local-fixture-only", SessionToken: "synthetic-temporary-token"}, now)
	const expected = "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE0000000000/20261021/us-east-1/ec2/aws4_request, SignedHeaders=content-type;host;x-amz-date;x-amz-security-token, Signature=19f94425fab9f2464fa7c8abb1c743996627df4ba525af3fe330bb742d52451d"
	if req.Header.Get("Authorization") != expected {
		t.Fatal("SigV4 request differs from independent protocol golden")
	}
	if req.Header.Get("X-Amz-Security-Token") != "synthetic-temporary-token" {
		t.Fatal("temporary session token missing")
	}
}
func TestAWSPaginatedRealHTTP(t *testing.T) {
	var calls atomic.Int32
	p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("X-Amz-Date") == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=") {
			t.Error("unsigned provider request")
			w.WriteHeader(403)
			return
		}
		if e := r.ParseForm(); e != nil {
			t.Fatal(e)
		}
		switch r.Form.Get("Action") {
		case "GetCallerIdentity":
			if r.Host != "sts.us-east-1.amazonaws.com" {
				t.Error("STS endpoint")
			}
			caller(w)
		case "DescribeInstances":
			calls.Add(1)
			if r.Host != "ec2.us-east-1.amazonaws.com" || r.Form.Get("Version") != ec2Version || r.Form.Get("MaxResults") != "100" {
				t.Error("EC2 protocol")
			}
			switch r.Form.Get("NextToken") {
			case "":
				fmt.Fprint(w, page("i-0123456789abcdef0", "page+2/="))
			case "page+2/=":
				fmt.Fprint(w, page("i-0123456789abcdef1", ""))
			default:
				t.Error("token not preserved")
			}
		default:
			t.Error("unexpected cloud action")
		}
	})
	items, e := p.Discover(context.Background(), a, "us-east-1")
	if e != nil || len(items) != 2 || calls.Load() != 2 || items[0].AvailabilityZone != "us-east-1a" {
		t.Fatalf("pagination failed: %d %v", len(items), e)
	}
}
func TestAWSFailureBoundaries(t *testing.T) {
	cases := []struct {
		name, body, code string
		status           int
	}{
		{"owner", strings.Replace(page("i-0123456789abcdef0", ""), "123456789012", "111111111111", 1), "AWS_ACCOUNT_MISMATCH", 200},
		{"malformed", "<broken>", "AWS_RESPONSE_INVALID", 200},
		{"expired", `<Response><Errors><Error><Code>ExpiredToken</Code><Message>secret-sentinel</Message></Error></Errors></Response>`, "AWS_CREDENTIALS_EXPIRED", 400},
		{"denied", `<Response><Errors><Error><Code>UnauthorizedOperation</Code></Error></Errors></Response>`, "AWS_ACCESS_DENIED", 403},
		{"throttle", `<Response><Errors><Error><Code>RequestLimitExceeded</Code></Error></Errors></Response>`, "AWS_THROTTLED", 503},
		{"oversized", strings.Repeat("x", 4097), "AWS_RESPONSE_INVALID", 200},
		{"repeat", page("", "again"), "AWS_PAGINATION_INVALID", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var calls atomic.Int32
			p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				if r.Form.Get("Action") == "GetCallerIdentity" {
					caller(w)
					return
				}
				calls.Add(1)
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			})
			if c.name == "oversized" {
				p.Policy.MaximumResponseBytes = 4096
			}
			items, e := p.Discover(context.Background(), a, "us-east-1")
			wantCode(t, e, c.code)
			if items != nil {
				t.Fatal("partial provider result escaped")
			}
			if calls.Load() > 2 {
				t.Fatal("retry bound")
			}
		})
	}
	t.Run("identity_account", func(t *testing.T) {
		p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Account>111111111111</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
		})
		_, e := p.Discover(context.Background(), a, "us-east-1")
		wantCode(t, e, "AWS_ACCOUNT_MISMATCH")
	})
	t.Run("scope_before_network", func(t *testing.T) {
		p, a := providerFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected network") })
		_, e := p.Discover(context.Background(), a, "eu-west-1")
		wantCode(t, e, "AWS_SCOPE_REJECTED")
	})
	t.Run("network", func(t *testing.T) {
		p, a := providerFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected network") })
		p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private path sentinel") })
		_, e := p.Discover(context.Background(), a, "us-east-1")
		wantCode(t, e, "AWS_UNAVAILABLE")
	})
	t.Run("cancelled", func(t *testing.T) {
		p, a := providerFixture(t, func(http.ResponseWriter, *http.Request) {})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, e := p.Discover(ctx, a, "us-east-1")
		wantCode(t, e, "AWS_CANCELLED")
	})
	t.Run("maximum_pages", func(t *testing.T) {
		p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
			r.ParseForm()
			if r.Form.Get("Action") == "GetCallerIdentity" {
				caller(w)
			} else {
				fmt.Fprint(w, page("", "more"))
			}
		})
		p.Policy.MaximumPages = 1
		_, e := p.Discover(context.Background(), a, "us-east-1")
		wantCode(t, e, "AWS_LIMIT_REACHED")
	})
}
func TestAWSCredentialFileProtection(t *testing.T) {
	path := credentialsFixture(t)
	c, e := readCredentials(path, time.Now())
	if e != nil || c.AccessKeyID == "" {
		t.Fatal("private credentials", e)
	}
	if os.Chmod(path, 0644) != nil {
		t.Fatal("fixture chmod")
	}
	_, e = readCredentials(path, time.Now())
	wantCode(t, e, "AWS_CREDENTIALS_INVALID")
	os.Chmod(path, 0600)
	raw, _ := json.Marshal(Credentials{AccessKeyID: "AKIAEXAMPLE0000000000", SecretAccessKey: "synthetic-secret-for-local-fixture-only", SessionToken: "temporary", ExpiresAt: time.Now().Add(-time.Second)})
	os.WriteFile(path, raw, 0600)
	_, e = readCredentials(path, time.Now())
	wantCode(t, e, "AWS_CREDENTIALS_EXPIRED")
	os.Remove(path)
	os.Symlink("/dev/null", path)
	_, e = readCredentials(path, time.Now())
	wantCode(t, e, "AWS_CREDENTIALS_INVALID")
}
func TestAWSRetryAfterTransientReadFailure(t *testing.T) {
	var calls atomic.Int32
	p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("Action") == "GetCallerIdentity" {
			caller(w)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			io.WriteString(w, `<Response/>`)
		} else {
			fmt.Fprint(w, page("", ""))
		}
	})
	out, e := p.Discover(context.Background(), a, "us-east-1")
	if e != nil || out == nil || len(out) != 0 || calls.Load() != 2 {
		t.Fatal("bounded transient retry", e)
	}
}
