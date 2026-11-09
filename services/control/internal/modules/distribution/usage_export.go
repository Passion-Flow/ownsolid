package distribution

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/url"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

// UsageExport holds a complete CSV page and the cursor for the next authorized page.
type UsageExport struct {
	CSV        []byte
	NextCursor *string
}

// ExportUsagePage exports one bounded history page using the same access checks,
// key filter and canonical receipt validation as RequestPage.
func (s *Service) ExportUsagePage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (UsageExport, error) {
	page, err := s.RequestPage(ctx, a, workspace, values)
	if err != nil {
		return UsageExport{}, err
	}

	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	writer.UseCRLF = true
	if err := writer.Write([]string{
		"request_id", "admitted_at", "request_state", "outcome_status", "request_count",
		"elapsed_ms", "prompt_tokens", "completion_tokens", "total_tokens",
	}); err != nil {
		return UsageExport{}, err
	}
	for _, record := range page.Items {
		var status, elapsed, prompt, completion, total string
		if outcome := record.Outcome; outcome != nil {
			status = outcome.Status
			elapsed = strconv.FormatUint(outcome.ElapsedMilliseconds, 10)
			if usage := outcome.Usage; usage != nil {
				prompt = strconv.FormatUint(uint64(usage.PromptTokens), 10)
				completion = strconv.FormatUint(uint64(usage.CompletionTokens), 10)
				total = strconv.FormatUint(uint64(usage.TotalTokens), 10)
			}
		}
		if err := writer.Write([]string{
			record.ID, record.AdmittedAt.Format(time.RFC3339Nano), record.State, status,
			"1", elapsed, prompt, completion, total,
		}); err != nil {
			return UsageExport{}, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return UsageExport{}, err
	}
	return UsageExport{CSV: buffer.Bytes(), NextCursor: page.NextCursor}, nil
}
