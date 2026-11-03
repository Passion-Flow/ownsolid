package mail

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Summary struct {
	ID            string  `json:"id"`
	State         string  `json:"state"`
	AttemptCount  int     `json:"attempt_count"`
	ErrorCategory *string `json:"error_category"`
}
type Input struct {
	Purpose, Recipient, Locale, InvitationID, ResetID, UserID, Token, Audience string
	Generation                                                                 int
}
type Payload struct {
	Link string `json:"link"`
}
type Outbox struct {
	DB        *pgxpool.Pool
	Storage   transaction.Store
	Keys      *Keyring
	Config    config.Config
	Templates map[string]Template
}
type claimed struct {
	ID, Purpose, Recipient, Locale, Key, Lease string
	MessageID                                  string
	Nonce, Cipher                              []byte
	Attempts                                   int
}

func (o *Outbox) DispatchOne(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(o.Config.Policy().Mail.AttemptSeconds))
	defer cancel()
	store, e := o.storage()
	if e != nil {
		return false, e
	}
	c, found, e := o.claim(ctx, store)
	if e != nil || !found {
		return false, e
	}
	eligible, e := o.eligible(ctx, store, c)
	if e != nil {
		return true, e
	}
	if !eligible {
		return true, o.finish(ctx, store, c, "cancelled", "token_obsolete")
	}
	b, e := o.Keys.Decrypt(c.Key, c.ID, c.Purpose, c.Nonce, c.Cipher)
	if e != nil {
		return true, o.finish(ctx, store, c, "failed", e.Error())
	}
	var payload Payload
	if e = json.Unmarshal(b, &payload); e != nil {
		return true, o.finish(ctx, store, c, "failed", "mail_payload_invalid")
	}
	message, e := o.Render(c.ID, c.Purpose, c.Locale, c.Recipient, payload, c.MessageID)
	if e != nil {
		return true, o.finish(ctx, store, c, "failed", "mail_template_invalid")
	}
	if e = Send(ctx, o.Config, c.Recipient, message); e != nil {
		category, permanent := Classify(e)
		if permanent || c.Attempts >= o.Config.Policy().Mail.MaxAttempts {
			return true, o.finish(ctx, store, c, "failed", category)
		}
		policy := o.Config.Policy().Mail
		delays := []int{policy.Retry1Seconds, policy.Retry2Seconds, policy.Retry3Seconds, policy.Retry4Seconds}
		delay := delays[c.Attempts-1]
		jitter, e := rand.Int(rand.Reader, big.NewInt(int64(delay*policy.JitterPercent*10)+1))
		if e != nil {
			return true, e
		}
		wait := time.Duration(delay)*time.Second + time.Duration(jitter.Int64())*time.Millisecond
		return true, o.requeue(ctx, store, c, wait, category)
	}
	return true, o.finish(ctx, store, c, "sent", "")
}
func (o *Outbox) Run(ctx context.Context) {
	ticker := time.NewTicker(config.Milliseconds(o.Config.Policy().Mail.PollMilliseconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := o.DispatchOne(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Error("mail_dispatch_failed", "error_category", "storage_unavailable")
			}
		}
	}
}
