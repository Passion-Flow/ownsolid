package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const relayCapabilityHeader = "X-Ownsolid-Idempotency"
const relayCapabilityVersion = "durable-v1"
const idempotencyKeyHeader = "Idempotency-Key"

type textBotMessage struct {
	MsgType string         `json:"msgtype"`
	Text    textBotContent `json:"text"`
}

type textBotContent struct {
	Content string `json:"content"`
}

type durableBotContract struct {
	codeField       string
	transientCodes  []int
	retry, rejected deliveryOutcome
}

func (s *webhookRelay) sendDurableBot(ctx context.Context, contract durableBotContract, probeURL, postURL string, raw []byte, transaction string) deliveryOutcome {
	ctx, cancel := context.WithTimeout(ctx, s.settings.timeout)
	defer cancel()

	probe, err := http.NewRequestWithContext(ctx, http.MethodOptions, probeURL, nil)
	if err != nil {
		return contract.rejected
	}
	response, err := s.client.Do(probe)
	if err != nil {
		return contract.retry
	}
	response.Body.Close()
	if retryableRelayStatus(response.StatusCode) {
		return contract.retry
	}
	if response.StatusCode != http.StatusNoContent || !oneHeader(response.Header, relayCapabilityHeader, relayCapabilityVersion) {
		return contract.rejected
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(raw))
	if err != nil {
		return contract.rejected
	}
	key := strconv.Quote(transaction)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(idempotencyKeyHeader, key)
	response, err = s.client.Do(request)
	if err != nil {
		return contract.retry
	}
	defer response.Body.Close()
	if retryableRelayStatus(response.StatusCode) {
		return contract.retry
	}
	if response.StatusCode != http.StatusOK {
		return contract.rejected
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRelayReceipt+1))
	var fields map[string]json.RawMessage
	if err != nil || len(data) > maxRelayReceipt || release.UniqueJSON(data, &fields) != nil {
		return contract.retry
	}
	var code *int
	if json.Unmarshal(fields[contract.codeField], &code) != nil || code == nil {
		return contract.retry
	}
	if *code != 0 {
		if slices.Contains(contract.transientCodes, *code) {
			return contract.retry
		}
		return contract.rejected
	}
	if !oneHeader(response.Header, idempotencyKeyHeader, key) {
		return contract.retry
	}
	return delivered
}

func oneHeader(headers http.Header, name, expected string) bool {
	values := headers.Values(name)
	return len(values) == 1 && values[0] == expected
}
