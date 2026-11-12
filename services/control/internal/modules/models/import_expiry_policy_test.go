package models

import (
	"context"
	"testing"
	"time"
)

func TestImportExpiryPolicyValidation(t *testing.T) {
	const workspace = "fa6c8e02-e5a5-42f4-8c90-6c7b6e6b5d78"
	for _, tc := range []struct {
		name, workspace string
		inactive        time.Duration
		invalid         bool
	}{
		{name: "minimum", workspace: workspace, inactive: time.Hour},
		{name: "whole_seconds", workspace: workspace, inactive: time.Hour + time.Second},
		{name: "maximum", workspace: workspace, inactive: 365 * 24 * time.Hour},
		{name: "missing_workspace", inactive: time.Hour, invalid: true},
		{name: "zero_workspace", workspace: "00000000-0000-0000-0000-000000000000", inactive: time.Hour, invalid: true},
		{name: "noncanonical_workspace", workspace: "FA6C8E02-E5A5-42F4-8C90-6C7B6E6B5D78", inactive: time.Hour, invalid: true},
		{name: "malformed_workspace", workspace: "workspace", inactive: time.Hour, invalid: true},
		{name: "zero_interval", workspace: workspace, invalid: true},
		{name: "negative_interval", workspace: workspace, inactive: -time.Hour, invalid: true},
		{name: "below_minimum", workspace: workspace, inactive: time.Hour - time.Second, invalid: true},
		{name: "above_maximum", workspace: workspace, inactive: 365*24*time.Hour + time.Second, invalid: true},
		{name: "fractional_seconds", workspace: workspace, inactive: time.Hour + time.Nanosecond, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (ImportExpiryPolicy{WorkspaceID: tc.workspace, InactiveFor: tc.inactive}).Validate()
			if (err != nil) != tc.invalid || err != nil && err.Error() != "MODEL_IMPORT_EXPIRY_POLICY_INVALID" {
				t.Fatalf("policy validation: %v", err)
			}
		})
	}
}

func TestModelImportWorkerAlreadyStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Starting after cancellation must not open a repository or model store.
	(&Service{}).Run(ctx)
}
