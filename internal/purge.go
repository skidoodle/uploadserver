package internal

import (
	"log/slog"
	"sync"
	"time"
)

// PurgeScheduler runs a background worker checking for due media purges.
type PurgeScheduler struct {
	store        *TokenStore
	purgeHandler func(tokenID string) error
	interval     time.Duration
	stop         chan struct{}
	wg           sync.WaitGroup
}

// NewPurgeScheduler creates a scheduler for processing due media purges.
func NewPurgeScheduler(store *TokenStore, purgeHandler func(tokenID string) error) *PurgeScheduler {
	return &PurgeScheduler{
		store:        store,
		purgeHandler: purgeHandler,
		interval:     30 * time.Second,
		stop:         make(chan struct{}),
	}
}

// Start launches the background goroutine.
func (s *PurgeScheduler) Start() {
	s.wg.Add(1)
	go s.run()
}

// Stop signals the background goroutine to exit and waits for it to finish.
func (s *PurgeScheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
}

// ProcessNow manually triggers a check for due purges (useful in tests).
func (s *PurgeScheduler) ProcessNow() (int, error) {
	return s.store.ProcessDuePurges(time.Now().UTC(), s.purgeHandler)
}

func (s *PurgeScheduler) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			count, err := s.ProcessNow()
			if err != nil {
				slog.Error("purge scheduler error", "error", err)
			} else if count > 0 {
				slog.Info("purge scheduler processed due purges", "count", count)
			}
		}
	}
}
