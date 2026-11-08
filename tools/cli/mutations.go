package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// The Admin contract limits counters to integers that JavaScript preserves exactly.
const maxResourceVersion = 9007199254740991

func requiredText(value, label string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > limit {
		return "", fmt.Errorf("%s must contain between 1 and %d characters and no NUL bytes", label, limit)
	}
	return value, nil
}

func requireID(value, label string) error {
	if !resourceID.MatchString(value) {
		return fmt.Errorf("%s must be a lowercase UUID", label)
	}
	return nil
}

func requireVersion(value int64, flag string) error {
	if value < 1 || value >= maxResourceVersion {
		return fmt.Errorf("%s must be the positive, incrementable version from the resource's JSON output", flag)
	}
	return nil
}

func (c *apiClient) mutation(ctx context.Context, input request, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	input.body = body
	if err := c.prepareWrite(ctx, &input); err != nil {
		return err
	}
	err = c.request(ctx, input, out)
	if err == nil {
		return nil
	}
	var problem *apiError
	if errors.As(err, &problem) && problem.status < 500 && problem.code != "STORAGE_COMMIT_OUTCOME_UNKNOWN" {
		return err
	}
	if input.key != "" {
		return fmt.Errorf("write result uncertain; retry with the same --idempotency-key %s and unchanged arguments: %w", input.key, err)
	}
	return fmt.Errorf("write result uncertain; inspect the resource before retrying: %w", err)
}

func (opts *options) renderItem(cmd *cobra.Command, columns []column, result json.RawMessage) error {
	return render(cmd.OutOrStdout(), opts.output, columns, result, []json.RawMessage{result})
}
