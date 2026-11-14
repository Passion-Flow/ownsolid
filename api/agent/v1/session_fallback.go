package v1

import "time"

// SessionFallbackDelay bounds HTTPS fallback after a previously usable stream.
// Initial unsupported paths retain their legacy window. Existing Agent pacing
// remains the retry owner; the remembered fact is scoped to one process session.
func SessionFallbackDelay(pacing Pacing, previouslyUsable bool) time.Duration {
	delay := time.Duration(SessionFallbackRetrySeconds) * time.Second
	if previouslyUsable && pacing.Validate() == nil {
		delay = min(delay, time.Duration(pacing.ReconnectMaxSeconds)*time.Second)
	}
	return delay
}
