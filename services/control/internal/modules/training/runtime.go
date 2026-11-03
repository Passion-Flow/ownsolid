package training

import (
	"context"
	"log/slog"
	"regexp"
	"time"
)

var controlErrorName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`)

// Run performs bounded control-plane work only; cancellation never guesses that a remote GPU is stopped.
// The caller must join it before closing the exclusive private store.
func (s *Service) Run(ctx context.Context) {
	var lastError string
	for ctx.Err() == nil {
		_, e := s.ReconcileNext(ctx)
		if e == nil {
			_, e = s.DispatchNext(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			code := e.Error()
			if !controlErrorName.MatchString(code) {
				code = "TRAINING_CONTROL_FAILED"
			}
			if code != lastError {
				slog.Error("training_control_iteration_failed", "code", code)
				lastError = code
			}
		} else {
			lastError = ""
		}
		timer := time.NewTimer(time.Duration(s.policy().PollMilliseconds) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
