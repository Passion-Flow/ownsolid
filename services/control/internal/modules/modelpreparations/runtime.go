package modelpreparations

import (
	"context"
	"log/slog"
	"regexp"
	"time"
)

var controlErrorName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`)

// Run bounds control work to one reconciliation and dispatch per poll; callers join before closing Store.
func (s *Service) Run(ctx context.Context) {
	var previous string
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
				code = "MODEL_PREPARATION_CONTROL_FAILED"
			}
			if code != previous {
				slog.Error("model_preparation_control_iteration_failed", "code", code)
				previous = code
			}
		} else {
			previous = ""
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
