// Package cloudinventory imports read-only provider observations into Workspace inventory.
package cloudinventory

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const ec2Version = "2016-11-15"
const stsVersion = "2011-06-15"

type Credentials struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	SessionToken    string    `json:"session_token,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}

func (Credentials) String() string   { return "AWS_CREDENTIALS_REDACTED" }
func (Credentials) GoString() string { return "AWS_CREDENTIALS_REDACTED" }

type Instance struct {
	InstanceID       string `json:"instance_id"`
	InstanceType     string `json:"instance_type"`
	State            string `json:"state"`
	AvailabilityZone string `json:"availability_zone"`
	PrivateIP        string `json:"private_ip"`
	PublicIP         string `json:"public_ip"`
}
type Provider struct {
	Policy config.AWSConfiguration
	client *http.Client
}

func NewProvider(p config.AWSConfiguration) *Provider {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &Provider{Policy: p, client: &http.Client{Transport: t, Timeout: time.Duration(p.RequestSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Close releases this provider's idle connections after request consumers exit.
func (p *Provider) Close()   { p.client.CloseIdleConnections() }
func fail(code string) error { return errors.New(code) }
func readCredentials(path string, now time.Time) (Credentials, error) {
	b, e := config.ReadAWSCredentials(path)
	if e != nil {
		return Credentials{}, e
	}
	defer clear(b)
	var c Credentials
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || !regexp.MustCompile(`^[A-Z0-9]{16,128}$`).MatchString(c.AccessKeyID) || len(c.SecretAccessKey) < 16 || len(c.SecretAccessKey) > 128 || strings.ContainsAny(c.SecretAccessKey, "\r\n") || len(c.SessionToken) > 8192 || strings.ContainsAny(c.SessionToken, "\r\n") || c.SessionToken != "" && c.ExpiresAt.IsZero() {
		return Credentials{}, fail("AWS_CREDENTIALS_INVALID")
	}
	if !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt) {
		return Credentials{}, fail("AWS_CREDENTIALS_EXPIRED")
	}
	return c, nil
}
func digest(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func mac(k []byte, v string) []byte {
	h := hmac.New(sha256.New, k)
	h.Write([]byte(v))
	return h.Sum(nil)
}
func sign(req *http.Request, body, region, service string, c Credentials, now time.Time) {
	stamp := now.UTC().Format("20060102T150405Z")
	date := stamp[:8]
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("X-Amz-Date", stamp)
	headers := []string{"content-type", "host", "x-amz-date"}
	if c.SessionToken != "" {
		headers = append(headers, "x-amz-security-token")
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
	}
	sort.Strings(headers)
	canonical := ""
	for _, h := range headers {
		v := req.Header.Get(h)
		if h == "host" {
			v = req.URL.Host
		}
		canonical += h + ":" + strings.Join(strings.Fields(v), " ") + "\n"
	}
	joined := strings.Join(headers, ";")
	request := req.Method + "\n/\n\n" + canonical + "\n" + joined + "\n" + digest(body)
	scope := date + "/" + region + "/" + service + "/aws4_request"
	key := mac(mac(mac(mac([]byte("AWS4"+c.SecretAccessKey), date), region), service), "aws4_request")
	signature := hex.EncodeToString(mac(key, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+digest(request)))
	clear(key)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKeyID+"/"+scope+", SignedHeaders="+joined+", Signature="+signature)
}
func endpoint(a config.AWSConnection, region, service string) string {
	suffix := "amazonaws.com"
	if a.Partition == "aws-cn" {
		suffix = "amazonaws.com.cn"
	}
	return "https://" + service + "." + region + "." + suffix + "/"
}
func (p *Provider) query(ctx context.Context, a config.AWSConnection, region, service string, values url.Values, c Credentials) ([]byte, error) {
	for attempt := 0; attempt < p.Policy.RetryAttempts; attempt++ {
		if !c.ExpiresAt.IsZero() && !time.Now().Before(c.ExpiresAt) {
			return nil, fail("AWS_CREDENTIALS_EXPIRED")
		}
		body := values.Encode()
		req, e := http.NewRequestWithContext(ctx, "POST", endpoint(a, region, service), strings.NewReader(body))
		if e != nil {
			return nil, fail("AWS_CONFIGURATION_INVALID")
		}
		sign(req, body, region, service, c, time.Now())
		response, e := p.client.Do(req)
		retry := false
		code := "AWS_UNAVAILABLE"
		if e != nil {
			if ctx.Err() != nil {
				return nil, fail("AWS_CANCELLED")
			}
			retry = true
		} else {
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, int64(p.Policy.MaximumResponseBytes)+1))
			response.Body.Close()
			if readErr != nil {
				if ctx.Err() != nil {
					return nil, fail("AWS_CANCELLED")
				}
				retry = true
			} else if len(raw) > p.Policy.MaximumResponseBytes {
				return nil, fail("AWS_RESPONSE_INVALID")
			} else if response.StatusCode == 200 {
				return raw, nil
			} else {
				var problem struct {
					Errors []struct {
						Code string `xml:"Code"`
					} `xml:"Errors>Error"`
					Error struct {
						Code string `xml:"Code"`
					} `xml:"Error"`
				}
				_ = xml.Unmarshal(raw, &problem)
				awsCode := problem.Error.Code
				if len(problem.Errors) > 0 {
					awsCode = problem.Errors[0].Code
				}
				switch awsCode {
				case "ExpiredToken", "ExpiredTokenException", "RequestExpired":
					return nil, fail("AWS_CREDENTIALS_EXPIRED")
				case "AuthFailure", "InvalidClientTokenId", "SignatureDoesNotMatch", "UnauthorizedOperation", "AccessDenied", "AccessDeniedException":
					return nil, fail("AWS_ACCESS_DENIED")
				case "RequestLimitExceeded", "Throttling", "ThrottlingException":
					retry = true
					code = "AWS_THROTTLED"
				}
				if response.StatusCode == 429 {
					retry = true
					code = "AWS_THROTTLED"
				}
				if response.StatusCode >= 500 {
					retry = true
				}
				if !retry {
					return nil, fail("AWS_PROVIDER_REJECTED")
				}
			}
		}
		if !retry || attempt+1 == p.Policy.RetryAttempts {
			return nil, fail(code)
		}
		delay := time.Duration(p.Policy.RetryMilliseconds) * (1 << attempt) * time.Millisecond
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fail("AWS_CANCELLED")
		case <-t.C:
		}
	}
	return nil, fail("AWS_UNAVAILABLE")
}
func (p *Provider) Discover(ctx context.Context, a config.AWSConnection, region string) ([]Instance, error) {
	if p.Policy.Validate() != nil || !config.AWSRegion(a.Partition, region) || !slices.Contains(a.Regions, region) {
		return nil, fail("AWS_SCOPE_REJECTED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Policy.ImportSeconds)*time.Second)
	defer cancel()
	c, e := readCredentials(a.CredentialsFile, time.Now())
	if e != nil {
		return nil, e
	}
	raw, e := p.query(ctx, a, region, "sts", url.Values{"Action": {"GetCallerIdentity"}, "Version": {stsVersion}}, c)
	if e != nil {
		return nil, e
	}
	var who struct {
		XMLName xml.Name `xml:"GetCallerIdentityResponse"`
		Result  struct {
			Account string `xml:"Account"`
			ARN     string `xml:"Arn"`
		} `xml:"GetCallerIdentityResult"`
	}
	if xml.Unmarshal(raw, &who) != nil || who.Result.Account != a.AccountID || !callerARN(who.Result.ARN, a) {
		return nil, fail("AWS_ACCOUNT_MISMATCH")
	}
	out := []Instance{}
	seen := map[string]bool{}
	tokens := map[string]bool{}
	token := ""
	for page := 0; page < p.Policy.MaximumPages; page++ {
		values := url.Values{"Action": {"DescribeInstances"}, "Version": {ec2Version}, "MaxResults": {strconv.Itoa(p.Policy.PageSize)}}
		if token != "" {
			values.Set("NextToken", token)
		}
		raw, e = p.query(ctx, a, region, "ec2", values, c)
		if e != nil {
			return nil, e
		}
		var result struct {
			XMLName      xml.Name `xml:"DescribeInstancesResponse"`
			Next         string   `xml:"nextToken"`
			Reservations []struct {
				Owner string `xml:"ownerId"`
				Items []struct {
					ID      string `xml:"instanceId"`
					Type    string `xml:"instanceType"`
					State   string `xml:"instanceState>name"`
					Zone    string `xml:"placement>availabilityZone"`
					Private string `xml:"privateIpAddress"`
					Public  string `xml:"ipAddress"`
				} `xml:"instancesSet>item"`
			} `xml:"reservationSet>item"`
		}
		if xml.Unmarshal(raw, &result) != nil || len(result.Next) > 8192 {
			return nil, fail("AWS_RESPONSE_INVALID")
		}
		for _, reservation := range result.Reservations {
			if reservation.Owner != a.AccountID {
				return nil, fail("AWS_ACCOUNT_MISMATCH")
			}
			for _, item := range reservation.Items {
				if !regexp.MustCompile(`^i-[a-f0-9]{8}([a-f0-9]{9})?$`).MatchString(item.ID) || seen[item.ID] || len(item.Type) > 128 || item.Type == "" || len(item.State) > 64 || len(item.Zone) > 128 || len(item.Private) > 64 || len(item.Public) > 64 {
					return nil, fail("AWS_RESPONSE_INVALID")
				}
				seen[item.ID] = true
				out = append(out, Instance{item.ID, item.Type, item.State, item.Zone, item.Private, item.Public})
				if len(out) > p.Policy.MaximumResources {
					return nil, fail("AWS_LIMIT_REACHED")
				}
			}
		}
		if result.Next == "" {
			sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
			return out, nil
		}
		if tokens[result.Next] {
			return nil, fail("AWS_PAGINATION_INVALID")
		}
		tokens[result.Next] = true
		token = result.Next
	}
	return nil, fail("AWS_LIMIT_REACHED")
}

// STS identifies the calling principal independently of the selected endpoint.
// IAM user/root and STS temporary principals have an empty region component.
func callerARN(value string, a config.AWSConnection) bool {
	parts := strings.SplitN(value, ":", 6)
	return len(value) <= 2048 && len(parts) == 6 && parts[0] == "arn" && parts[1] == a.Partition && (parts[2] == "iam" || parts[2] == "sts") && parts[3] == "" && parts[4] == a.AccountID && parts[5] != "" && !strings.ContainsAny(value, "\r\n\t ")
}
