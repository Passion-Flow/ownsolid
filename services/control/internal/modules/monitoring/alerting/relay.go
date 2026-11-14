package alerting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const webhookPath = "/v1/alertmanager/"
const maxWebhookBytes = 256 << 10
const maxRelayReceipt = 8 << 10

type deliveryOutcome string

const (
	delivered        deliveryOutcome = "RELAY_DELIVERED"
	invalidWebhook   deliveryOutcome = "RELAY_WEBHOOK_INVALID"
	scopeRejected    deliveryOutcome = "RELAY_SCOPE_REJECTED"
	matrixRetry      deliveryOutcome = "RELAY_MATRIX_RETRY"
	matrixRejected   deliveryOutcome = "RELAY_MATRIX_REJECTED"
	feishuRetry      deliveryOutcome = "RELAY_FEISHU_RETRY"
	feishuRejected   deliveryOutcome = "RELAY_FEISHU_REJECTED"
	dingTalkRetry    deliveryOutcome = "RELAY_DINGTALK_RETRY"
	dingTalkRejected deliveryOutcome = "RELAY_DINGTALK_REJECTED"
	weComRetry       deliveryOutcome = "RELAY_WECOM_RETRY"
	weComRejected    deliveryOutcome = "RELAY_WECOM_REJECTED"
)

var eventID = regexp.MustCompile(`^\$[A-Za-z0-9._=+/:~-]{1,254}$`)

type webhookRelay struct {
	settings relaySettings
	client   *http.Client
	slots    chan struct{}
	logger   *slog.Logger
}

// RunMatrixRelay serves authenticated tenant webhooks on the configured private
// TLS listener until the context is canceled. Alertmanager owns retry scheduling.
func RunMatrixRelay(ctx context.Context, configurationFile string, logger *slog.Logger) error {
	return runRelay(ctx, configurationFile, matrixChannel, logger)
}

// RunFeishuRelay serves authenticated tenant webhooks for a private Feishu
// receiver that advertises the ownsolid durable idempotency contract.
func RunFeishuRelay(ctx context.Context, configurationFile string, logger *slog.Logger) error {
	return runRelay(ctx, configurationFile, feishuChannel, logger)
}

// RunDingTalkRelay serves authenticated tenant webhooks for a private receiver
// that supports signed DingTalk messages and the ownsolid durable receipt contract.
func RunDingTalkRelay(ctx context.Context, configurationFile string, logger *slog.Logger) error {
	return runRelay(ctx, configurationFile, dingTalkChannel, logger)
}

// RunWeComRelay serves authenticated tenant webhooks for a private receiver
// that supports WeCom text messages and the ownsolid durable receipt contract.
func RunWeComRelay(ctx context.Context, configurationFile string, logger *slog.Logger) error {
	return runRelay(ctx, configurationFile, weComChannel, logger)
}

func runRelay(ctx context.Context, configurationFile string, channel relayChannel, logger *slog.Logger) error {
	settings, err := loadRelaySettings(configurationFile, channel)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", settings.listen)
	if err != nil {
		return err
	}
	relay := newWebhookRelay(settings, logger)
	defer relay.client.CloseIdleConnections()
	const readTimeout = 10 * time.Second
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{settings.certificate}}
	server := &http.Server{
		Handler: relay, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: readTimeout,
		WriteTimeout: readTimeout + settings.timeout + time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 8 << 10,
	}
	exited := make(chan error, 1)
	go func() {
		exited <- server.Serve(tls.NewListener(listener, tlsConfig))
	}()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), readTimeout+settings.timeout+time.Second)
		defer cancel()
		if err := server.Shutdown(stop); err != nil {
			_ = server.Close()
			<-exited
			return err
		}
		if err := <-exited; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func newWebhookRelay(settings relaySettings, logger *slog.Logger) *webhookRelay {
	transport := &http.Transport{
		TLSClientConfig:    &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: settings.roots},
		DisableCompression: true, MaxConnsPerHost: settings.concurrent, IdleConnTimeout: 30 * time.Second,
	}
	client := &http.Client{
		Transport: transport, Timeout: settings.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &webhookRelay{settings, client, make(chan struct{}, settings.concurrent), logger}
}

type relayDelivery struct {
	tenant string
	route  tenantRoute
	batch  relayWebhook
	text   string
}

func (s *webhookRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.serveWebhook(w, r, nil)
}

func (s *webhookRelay) serveWebhook(w http.ResponseWriter, r *http.Request, deliver func(context.Context, relayDelivery) deliveryOutcome) {
	tenant, matches := strings.CutPrefix(r.URL.Path, webhookPath)
	if !matches || !tenantID.MatchString(tenant) || r.URL.RawPath != "" || r.URL.ForceQuery || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "RELAY_METHOD_INVALID", http.StatusMethodNotAllowed)
		return
	}
	route, configured := s.settings.tenants[tenant]
	token, valid := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	candidate := sha256.Sum256([]byte(token))
	if !configured || !valid || len(r.Header.Values("Authorization")) != 1 ||
		subtle.ConstantTimeCompare(candidate[:], route.inbound[:]) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "RELAY_AUTH_REQUIRED", http.StatusUnauthorized)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "RELAY_BUSY", http.StatusServiceUnavailable)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "RELAY_CONTENT_TYPE_INVALID", http.StatusUnsupportedMediaType)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			http.Error(w, "RELAY_BODY_TOO_LARGE", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, string(invalidWebhook), http.StatusBadRequest)
		}
		return
	}
	var batch relayWebhook
	if release.UniqueJSON(data, &batch) != nil {
		http.Error(w, string(invalidWebhook), http.StatusBadRequest)
		return
	}
	started := time.Now()
	msg, transaction, result := mapWebhook(tenant, route, batch)
	if result == delivered {
		if deliver == nil {
			result = s.send(r.Context(), route, msg, transaction)
		} else {
			result = deliver(r.Context(), relayDelivery{tenant, route, batch, msg})
		}
	}
	s.logger.LogAttrs(r.Context(), logLevel(result), "alert relay delivery",
		slog.String("tenant_id", tenant), slog.String("outcome", string(result)),
		slog.String("transaction_id", transaction), slog.Int64("duration_ms", time.Since(started).Milliseconds()))
	switch result {
	case delivered:
		w.WriteHeader(http.StatusNoContent)
	case invalidWebhook:
		http.Error(w, string(result), http.StatusBadRequest)
	case scopeRejected:
		http.Error(w, string(result), http.StatusForbidden)
	case matrixRejected, feishuRejected, dingTalkRejected, weComRejected:
		http.Error(w, string(result), http.StatusUnprocessableEntity)
	default:
		http.Error(w, string(result), http.StatusServiceUnavailable)
	}
}

func logLevel(result deliveryOutcome) slog.Level {
	if result == delivered {
		return slog.LevelInfo
	}
	return slog.LevelWarn
}

func (s *webhookRelay) send(ctx context.Context, route tenantRoute, text, transaction string) deliveryOutcome {
	switch s.settings.channel {
	case feishuChannel:
		return s.sendFeishu(ctx, route, text, transaction)
	case dingTalkChannel:
		return s.sendDingTalk(ctx, route, text, transaction)
	case weComChannel:
		return s.sendWeCom(ctx, route, text, transaction)
	}
	raw, err := json.Marshal(matrixMessage{MsgType: "m.text", Body: text})
	if err != nil {
		return matrixRejected
	}
	path := s.settings.origin.String() + "/_matrix/client/v3/rooms/" + url.PathEscape(route.target) + "/send/m.room.message/" + transaction
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, path, bytes.NewReader(raw))
	if err != nil {
		return matrixRejected
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+route.outbound)
	res, err := s.client.Do(req)
	if err != nil {
		return matrixRetry
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusOK:
		var receipt struct {
			EventID string `json:"event_id"`
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, maxRelayReceipt+1))
		if err != nil || len(data) > maxRelayReceipt ||
			release.UniqueJSON(data, &receipt) != nil || !eventID.MatchString(receipt.EventID) {
			return matrixRetry
		}
		return delivered
	case retryableRelayStatus(res.StatusCode):
		return matrixRetry
	default:
		return matrixRejected
	}
}

func retryableRelayStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500
}
