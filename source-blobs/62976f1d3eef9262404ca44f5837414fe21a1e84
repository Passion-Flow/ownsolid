package datasets

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Run joins its bounded workers on shutdown; the caller must wait for it before releasing the exclusive store.
func (s *Service) Run(ctx context.Context) {
	var group sync.WaitGroup
	for i := 0; i < s.policy().Concurrency; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				worked, e := s.ProcessNext(ctx)
				if ctx.Err() != nil {
					return
				}
				if e != nil {
					slog.Error("dataset_worker_attempt_failed", "code", safeDatasetError(e))
				}
				if !worked || e != nil {
					timer := time.NewTimer(time.Duration(s.policy().PollMilliseconds) * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
		}()
	}
	group.Wait()
}
