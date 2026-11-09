package alerting

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const feishuHookPath = "/open-apis/bot/v2/hook/"
const relayCapabilityHeader = "X-Ownsolid-Idempotency"
const relayCapabilityVersion = "durable-v1"
const idempotencyKeyHeader = "Idempotency-Key"
const feishuRateLimited = 11232
const maxFeishuRequestBytes = 20_000

type feishuText struct {
	Text string `json:"text"`
}

type feishuMessage struct {
	Timestamp string     `json:"timestamp"`
	Sign      string     `json:"sign"`
	MsgType   string     `json:"msg_type"`
	Content   feishuText `json:"content"`
}

func signFeishu(timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (s *webhookRelay) sendFeishu(ctx context.Context, route tenantRoute, text, transaction string) deliveryOutcome {
	ctx, cancel := context.WithTimeout(ctx, s.settings.timeout)
	defer cancel()
	target := s.settings.origin.String() + feishuHookPath + route.target
	probe, err := http.NewRequestWithContext(ctx, http.MethodOptions, target, nil)
	if err != nil {
		return feishuRejected
	}
	response, err := s.client.Do(probe)
	if err != nil {
		return feishuRetry
	}
	response.Body.Close()
	if retryableRelayStatus(response.StatusCode) {
		return feishuRetry
	}
	if response.StatusCode != http.StatusNoContent || response.Header.Get(relayCapabilityHeader) != relayCapabilityVersion {
		return feishuRejected
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	raw, err := json.Marshal(feishuMessage{timestamp, signFeishu(timestamp, route.outbound), "text", feishuText{text}})
	if err != nil || len(raw) > maxFeishuRequestBytes {
		return feishuRejected
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return feishuRejected
	}
	key := strconv.Quote(transaction)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(idempotencyKeyHeader, key)
	response, err = s.client.Do(request)
	if err != nil {
		return feishuRetry
	}
	defer response.Body.Close()
	if retryableRelayStatus(response.StatusCode) {
		return feishuRetry
	}
	if response.StatusCode != http.StatusOK {
		return feishuRejected
	}
	var receipt struct {
		Code *int `json:"code"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRelayReceipt+1))
	if err != nil || len(data) > maxRelayReceipt || release.UniqueJSON(data, &receipt) != nil || receipt.Code == nil {
		return feishuRetry
	}
	switch *receipt.Code {
	case feishuRateLimited:
		return feishuRetry
	case 0:
		if response.Header.Get(idempotencyKeyHeader) != key {
			return feishuRetry
		}
		return delivered
	default:
		return feishuRejected
	}
}
