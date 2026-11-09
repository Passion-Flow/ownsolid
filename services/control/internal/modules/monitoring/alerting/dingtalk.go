package alerting

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

const dingTalkHookPath = "/robot/send"
const maxDingTalkRequestBytes = 16 << 10

var dingTalkContract = durableBotContract{"errcode", []int{-1, 410100}, dingTalkRetry, dingTalkRejected}

type dingTalkMessage struct {
	MsgType string       `json:"msgtype"`
	Text    dingTalkText `json:"text"`
}

type dingTalkText struct {
	Content string `json:"content"`
}

func signDingTalk(timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (s *webhookRelay) sendDingTalk(ctx context.Context, route tenantRoute, text, transaction string) deliveryOutcome {
	raw, err := json.Marshal(dingTalkMessage{"text", dingTalkText{text}})
	if err != nil || len(raw) > maxDingTalkRequestBytes {
		return dingTalkRejected
	}
	target := *s.settings.origin
	target.Path = dingTalkHookPath
	probeURL := target.String()
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	target.RawQuery = url.Values{
		"access_token": {route.target},
		"timestamp":    {timestamp},
		"sign":         {signDingTalk(timestamp, route.outbound)},
	}.Encode()
	return s.sendDurableBot(ctx, dingTalkContract, probeURL, target.String(), raw, transaction)
}
