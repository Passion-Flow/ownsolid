package v1

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestPreparationRemovalStrictChallengeReceiptBindingAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	command := validCommand(t)
	digest, _ := command.Digest()
	c := RemovalChallenge{SchemaVersion: 1, ChallengeID: command.AttemptID, NodeID: command.ModelID, TaskID: command.HeldoutID, Command: command, CommandSHA256: digest, IssuedAt: now, ExpiresAt: now.Add(RemovalChallengeLifetime)}
	r := RemovalReceipt{SchemaVersion: 1, ReceiptID: command.AttemptID, Challenge: c, ChallengeSHA256: c.Digest(), ObservedAt: now, Stop: StopReceipt{SchemaVersion: 1, ReceiptID: command.AttemptID, JournalID: command.AttemptID, NodeID: c.NodeID, TaskID: c.TaskID, Scope: command.Scope, AttemptID: command.AttemptID, AttemptNumber: command.AttemptNumber, CommandSHA256: digest, ImageID: *command.Recipe.Identity.ImageID, GPUUUID: command.GPUUUID, ContainerName: ContainerName(command.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, CompletedAt: now}}
	raw, _ := json.Marshal(r)
	if _, e := DecodeRemovalReceipt(raw, now); e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"expired", "early_observation", "future", "digest", "task", "gpu", "container", "duplicate", "case", "null", "missing", "unknown"} {
		t.Run(fault, func(t *testing.T) {
			value := r
			clock := now
			switch fault {
			case "expired":
				clock = now.Add(RemovalChallengeLifetime)
			case "early_observation":
				value.ObservedAt = now.Add(-time.Second)
			case "future":
				value.ObservedAt = now.Add(RemovalClockSkew + time.Second)
			case "digest":
				value.ChallengeSHA256 = command.ModelCatalogSHA256
			case "task":
				value.Stop.TaskID = "00000000-0000-4000-8000-000000000099"
			case "gpu":
				value.Stop.GPUUUID += "x"
			case "container":
				value.Stop.ContainerName += "x"
			}
			input, _ := json.Marshal(value)
			switch fault {
			case "duplicate":
				input = append([]byte(`{"schema_version":1,`), input[1:]...)
			case "case":
				input = bytes.Replace(input, []byte(`"observed_at"`), []byte(`"Observed_at"`), 1)
			case "null":
				input = bytes.Replace(input, []byte(`"schema_version":1`), []byte(`"schema_version":null`), 1)
			case "missing":
				input = bytes.Replace(input, []byte(`"schema_version":1,`), nil, 1)
			case "unknown":
				input = append([]byte(`{"extra":true,`), input[1:]...)
			}
			if _, e := DecodeRemovalReceipt(input, clock); e == nil {
				t.Fatal("invalid operator removal accepted")
			}
		})
	}
}
