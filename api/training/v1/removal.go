package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const MaximumRemovalBytes = 131072
const RemovalChallengeLifetime = 7 * 24 * time.Hour
const RemovalClockSkew = 5 * time.Minute

type RemovalChallenge struct {
	SchemaVersion int       `json:"schema_version"`
	ChallengeID   string    `json:"challenge_id"`
	NodeID        string    `json:"node_id"`
	TaskID        string    `json:"task_id"`
	Command       Command   `json:"command"`
	CommandSHA256 string    `json:"command_sha256"`
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (c RemovalChallenge) Validate(now time.Time) error {
	digest, e := c.Command.Digest()
	if e != nil || c.SchemaVersion != 1 || !datawire.ValidID(c.ChallengeID) || !datawire.ValidID(c.NodeID) || !datawire.ValidID(c.TaskID) || c.CommandSHA256 != digest || c.IssuedAt.Year() < 2020 || c.IssuedAt.Year() > 2200 || c.IssuedAt.After(now.Add(RemovalClockSkew)) || !c.ExpiresAt.Equal(c.IssuedAt.Add(RemovalChallengeLifetime)) || !now.Before(c.ExpiresAt) {
		return errors.New("TRAINING_REMOVAL_CHALLENGE_INVALID")
	}
	return nil
}
func (c RemovalChallenge) Digest() string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type RemovalReceipt struct {
	SchemaVersion   int              `json:"schema_version"`
	ReceiptID       string           `json:"receipt_id"`
	Challenge       RemovalChallenge `json:"challenge"`
	ChallengeSHA256 string           `json:"challenge_sha256"`
	Stop            StopReceipt      `json:"stop"`
	ObservedAt      time.Time        `json:"observed_at"`
}

func (r RemovalReceipt) Validate(now time.Time) error {
	if r.SchemaVersion != 1 || !datawire.ValidID(r.ReceiptID) || r.Challenge.Validate(now) != nil || r.ChallengeSHA256 != r.Challenge.Digest() || r.Stop.ValidateFor(r.Challenge.Command, r.Challenge.NodeID, r.Challenge.TaskID) != nil || r.Stop.CompletedAt.After(r.ObservedAt) || r.ObservedAt.Before(r.Challenge.IssuedAt) || r.ObservedAt.After(now.Add(RemovalClockSkew)) {
		return errors.New("TRAINING_REMOVAL_RECEIPT_INVALID")
	}
	return nil
}
func DecodeRemovalChallenge(raw []byte, now time.Time) (RemovalChallenge, error) {
	var c RemovalChallenge
	if len(raw) > MaximumRemovalBytes || DecodeStrict(raw, &c) != nil || c.Validate(now) != nil {
		return c, errors.New("TRAINING_REMOVAL_CHALLENGE_INVALID")
	}
	return c, nil
}
func DecodeRemovalReceipt(raw []byte, now time.Time) (RemovalReceipt, error) {
	var r RemovalReceipt
	if len(raw) > MaximumRemovalBytes || DecodeStrict(raw, &r) != nil || r.Validate(now) != nil {
		return r, errors.New("TRAINING_REMOVAL_RECEIPT_INVALID")
	}
	return r, nil
}
