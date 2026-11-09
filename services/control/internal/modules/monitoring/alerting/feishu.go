package alerting

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"
)

const feishuHookPath = "/open-apis/bot/v2/hook/"
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

var feishuContract = durableBotContract{"code", []int{11232}, feishuRetry, feishuRejected}

func (s *webhookRelay) sendFeishu(ctx context.Context, route tenantRoute, text, transaction string) deliveryOutcome {
	target := s.settings.origin.String() + feishuHookPath + route.target
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	raw, err := json.Marshal(feishuMessage{timestamp, signFeishu(timestamp, route.outbound), "text", feishuText{text}})
	if err != nil || len(raw) > maxFeishuRequestBytes {
		return feishuRejected
	}
	return s.sendDurableBot(ctx, feishuContract, target, target, raw, transaction)
}
