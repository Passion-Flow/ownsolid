package alerting

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"
)

const weComHookPath = "/cgi-bin/webhook/send"
const maxWeComTextBytes = 2048

var weComContract = durableBotContract{"errcode", []int{-1, 45009}, weComRetry, weComRejected}

func (s *webhookRelay) sendWeCom(ctx context.Context, route tenantRoute, text, transaction string) deliveryOutcome {
	raw, err := json.Marshal(textBotMessage{"text", textBotContent{weComSummary(text)}})
	if err != nil {
		return weComRejected
	}
	target := *s.settings.origin
	target.Path = weComHookPath
	probeURL := target.String()
	target.RawQuery = url.Values{"key": {route.target}}.Encode()
	return s.sendDurableBot(ctx, weComContract, probeURL, target.String(), raw, transaction)
}

func weComSummary(text string) string {
	if len(text) <= maxWeComTextBytes {
		return text
	}
	const notice = "\n[Alert details shortened]"
	end := maxWeComTextBytes - len(notice)
	for !utf8.RuneStart(text[end]) {
		end--
	}
	if line := strings.LastIndexByte(text[:end], '\n'); line > 0 {
		end = line
	}
	return text[:end] + notice
}
