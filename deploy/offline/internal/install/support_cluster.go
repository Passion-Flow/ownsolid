package install

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const maxClusterResponse = 1 << 20

var (
	clusterSupportLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	clusterSupportToken = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
)

type clusterSupportInput struct {
	Version        int                     `json:"format_version"`
	TimeoutSeconds int                     `json:"timeout_seconds"`
	Management     *managementSupportInput `json:"management,omitempty"`
	Helm           *helmSupportInput       `json:"helm,omitempty"`
}

type clusterSupportEndpoint struct {
	Origin         string `json:"origin"`
	CAFile         string `json:"ca_file"`
	CredentialFile string `json:"credential_file"`
}

type managementSupportInput struct {
	clusterSupportEndpoint
	Site string `json:"site_id"`
	Cell string `json:"cell_id"`
}

type helmSupportInput struct {
	clusterSupportEndpoint
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
}

func (c clusterSupportEndpoint) valid() bool {
	u, err := originURL(c.Origin)
	return err == nil && !u.ForceQuery && canonicalPath(c.CAFile) && canonicalPath(c.CredentialFile)
}

func clusterLabel(value string, limit int) bool {
	return len(value) <= limit && clusterSupportLabel.MatchString(value)
}

// ClusterSupportBundle publishes a redacted report from explicitly selected read APIs.
func ClusterSupportBundle(ctx context.Context, input, output string, out io.Writer) error {
	raw, err := bundle.ReadProtectedConfiguration(input)
	if err != nil {
		return errors.New("INSTALL_CLUSTER_SUPPORT_INPUT_UNAVAILABLE: select a protected diagnostics configuration")
	}
	var c clusterSupportInput
	if wire.StrictJSON(raw, &c) != nil || c.Version != 1 || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 60 ||
		(c.Management == nil && c.Helm == nil) {
		return errors.New("INSTALL_CLUSTER_SUPPORT_INPUT_INVALID: set format version 1, a timeout of 1 to 60 seconds, and at least one source")
	}
	if m := c.Management; m != nil && (!m.valid() || !clusterLabel(m.Site, 32) || !clusterLabel(m.Cell, 32)) {
		return errors.New("INSTALL_CLUSTER_SUPPORT_MANAGEMENT_INVALID: set the Admin HTTPS origin, site, cell and protected files")
	}
	if h := c.Helm; h != nil && (!h.valid() || !clusterLabel(h.Namespace, 63) || !clusterLabel(h.Release, 53)) {
		return errors.New("INSTALL_CLUSTER_SUPPORT_HELM_INVALID: set the Kubernetes HTTPS origin, namespace, release and protected files")
	}
	return publishSupportBundle(ctx, output, time.Duration(c.TimeoutSeconds)*time.Second, out,
		func(admission context.Context) (*supportAccess, error) {
			return clusterSupportAccess(admission, input, raw)
		},
		func(collection context.Context, _ *supportAccess) (any, int) {
			report := collectClusterSupport(collection, c)
			return report, len(report.Issues)
		})
}

type clusterSupportReport struct {
	Version    int                      `json:"format_version"`
	Collected  time.Time                `json:"collected_at"`
	Scope      string                   `json:"scope"`
	Management *managementSupportState  `json:"management,omitempty"`
	Deployment *deploymentSupportState  `json:"helm_deployment,omitempty"`
	Service    *helmServiceSupportState `json:"helm_service,omitempty"`
	Issues     []string                 `json:"collection_issues"`
}

type managementSupportState struct {
	State   string            `json:"recorded_state"`
	Server  string            `json:"recorded_server_state,omitempty"`
	Members map[string]uint32 `json:"members,omitempty"`
}

type clusterSupportClient struct {
	origin     string
	client     *http.Client
	credential string
	cookie     string
}

func newClusterSupportClient(c clusterSupportEndpoint, cookie string) (*clusterSupportClient, string) {
	ca, err := bundle.ReadProtectedConfiguration(c.CAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		return nil, "TRUST_UNAVAILABLE"
	}
	raw, err := bundle.ReadProtectedConfiguration(c.CredentialFile)
	credential := strings.TrimSpace(string(raw))
	if err != nil || len(credential) > 8192 || !clusterSupportToken.MatchString(credential) {
		return nil, "CREDENTIAL_UNAVAILABLE"
	}
	return &clusterSupportClient{c.Origin, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, credential, cookie}, ""
}

func (c *clusterSupportClient) get(ctx context.Context, path string, out any) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return "INVALID_REQUEST"
	}
	req.Header.Set("Accept", "application/json")
	if c.cookie == "" {
		req.Header.Set("Authorization", "Bearer "+c.credential)
	} else {
		req.Header.Set("Origin", c.origin)
		req.AddCookie(&http.Cookie{Name: c.cookie, Value: c.credential})
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "TIMEOUT"
		}
		var trustErr *tls.CertificateVerificationError
		if errors.As(err, &trustErr) {
			return "TLS_REJECTED"
		}
		return "UNAVAILABLE"
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "ACCESS_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "TIMEOUT"
	default:
		return "UNAVAILABLE"
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxClusterResponse+1))
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "TIMEOUT"
	}
	if err != nil || len(raw) > maxClusterResponse || wire.UniqueJSON(raw, out) != nil {
		return "INVALID_RESPONSE"
	}
	return ""
}

func collectClusterSupport(ctx context.Context, c clusterSupportInput) clusterSupportReport {
	report := clusterSupportReport{Version: 1, Collected: time.Now().UTC(), Scope: "managed_cluster", Issues: []string{}}
	var wg sync.WaitGroup
	var managementIssue, helmIssue, deploymentIssue, serviceIssue string
	if m := c.Management; m != nil {
		wg.Go(func() { report.Management, managementIssue = collectManagementSupport(ctx, m) })
	}
	if h := c.Helm; h != nil {
		client, issue := newClusterSupportClient(h.clusterSupportEndpoint, "")
		helmIssue = issue
		if client != nil {
			wg.Go(func() { report.Deployment, deploymentIssue = h.deployment(ctx, client) })
			wg.Go(func() { report.Service, serviceIssue = h.service(ctx, client) })
		}
	}
	wg.Wait()
	for _, issue := range []struct{ prefix, code string }{
		{"MANAGED_CLUSTER_", managementIssue}, {"HELM_", helmIssue},
		{"HELM_DEPLOYMENT_", deploymentIssue}, {"HELM_SERVICE_", serviceIssue},
	} {
		if issue.code != "" {
			report.Issues = append(report.Issues, issue.prefix+issue.code)
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		report.Issues = append(report.Issues, "COLLECTION_TIMEOUT")
	}
	return report
}

func collectManagementSupport(ctx context.Context, m *managementSupportInput) (*managementSupportState, string) {
	client, issue := newClusterSupportClient(m.clusterSupportEndpoint, "__Host-admin_session")
	if client == nil {
		return nil, issue
	}
	var response struct {
		Data struct {
			Items []struct {
				Site    string             `json:"site_id"`
				Cell    string             `json:"cell_id"`
				State   string             `json:"state"`
				Server  string             `json:"server_state"`
				Members map[string]*uint32 `json:"members"`
			} `json:"items"`
			Cursor *string `json:"next_cursor"`
		} `json:"data"`
	}
	if issue := client.get(ctx, "/api/v1/sites/"+m.Site+"/cells/"+m.Cell+"/admin/cell-clusters", &response); issue != "" {
		return nil, issue
	}
	items := response.Data.Items
	if items == nil || len(items) > 1 || response.Data.Cursor != nil {
		return nil, "INVALID_RESPONSE"
	}
	if len(items) == 0 {
		return &managementSupportState{State: "absent"}, ""
	}
	item := items[0]
	memberStates := []string{"pending", "joining", "ready", "not_ready", "failed"}
	if item.Site != m.Site || item.Cell != m.Cell ||
		!slices.Contains([]string{"installing", "ready", "degraded", "failed"}, item.State) ||
		!slices.Contains(memberStates, item.Server) || len(item.Members) != len(memberStates) {
		return nil, "INVALID_RESPONSE"
	}
	report := &managementSupportState{State: item.State, Server: item.Server, Members: make(map[string]uint32, len(memberStates))}
	for _, state := range memberStates {
		count := item.Members[state]
		if count == nil {
			return nil, "INVALID_RESPONSE"
		}
		report.Members[state] = *count
	}
	return report, ""
}
