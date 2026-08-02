package internal

import (
	"log"
	"sync"
	"time"
)

// InviteScheduler runs background jobs for scheduled invite giveaways
// and processing pending new-member grants.
type InviteScheduler struct {
	store *TokenStore
	stop  chan struct{}
	wg    sync.WaitGroup
}

// NewInviteScheduler creates a scheduler bound to the given store.
func NewInviteScheduler(store *TokenStore) *InviteScheduler {
	return &InviteScheduler{
		store: store,
		stop:  make(chan struct{}),
	}
}

// Start launches the background goroutine. It reads the invite policy from the
// store on each cycle, so policy changes take effect without a server restart.
func (s *InviteScheduler) Start() {
	s.wg.Add(1)
	go s.run()
}

// Stop signals the background goroutine to exit and waits for it to finish.
func (s *InviteScheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
}

// run is the main loop of the invite scheduler.
// It reads the invite policy from the store on each cycle, so policy changes take effect without a server restart.
func (s *InviteScheduler) run() {
	defer s.wg.Done()

	// Check pending grants every 60 seconds.
	pendingTicker := time.NewTicker(60 * time.Second)
	defer pendingTicker.Stop()

	// For the scheduled giveaway, we track when the next run is due.
	var nextSchedRun time.Time
	s.updateNextRun(&nextSchedRun)

	for {
		select {
		case <-s.stop:
			return
		case <-pendingTicker.C:
			s.processPending()
			s.checkScheduled(&nextSchedRun)
		}
	}
}

// updateNextRun reads the policy and sets the next scheduled run time.
// If the policy is disabled, the next run is set to the zero time.
func (s *InviteScheduler) updateNextRun(next *time.Time) {
	pol := s.store.InvitePolicy()
	if !pol.SchedEnabled || pol.SchedInterval <= 0 || pol.SchedCount <= 0 {
		*next = time.Time{} // disabled
		return
	}
	if next.IsZero() {
		*next = time.Now().Add(time.Duration(pol.SchedInterval) * time.Second)
	}
}

// checkScheduled runs the scheduled giveaway if it's due.
// If the policy is disabled, the next run is set to the zero time.
func (s *InviteScheduler) checkScheduled(next *time.Time) {
	pol := s.store.InvitePolicy()
	if !pol.SchedEnabled || pol.SchedInterval <= 0 || pol.SchedCount <= 0 {
		*next = time.Time{} // disabled
		return
	}

	// If we haven't set a next run yet, set one.
	if next.IsZero() {
		*next = time.Now().Add(time.Duration(pol.SchedInterval) * time.Second)
		return
	}

	if time.Now().Before(*next) {
		return // not yet due
	}

	// Execute the giveaway.
	var updated int
	var err error
	switch pol.SchedMode {
	case "random":
		pool := pol.SchedPool
		if pool <= 0 {
			pool = 1
		}
		updated, err = s.store.AddInvitesToRandomUploaders(pol.SchedCount, pool, pol.SchedMax)
	default: // "all" or empty
		updated, err = s.store.AddInvitesToAllUploadersCapped(pol.SchedCount, pol.SchedMax)
	}

	if err != nil {
		log.Printf("invite scheduler: giveaway error: %v", err)
	} else if updated > 0 {
		log.Printf("invite scheduler: gave %d invite(s) to %d user(s)", pol.SchedCount, updated)
	}

	// Schedule the next run.
	*next = time.Now().Add(time.Duration(pol.SchedInterval) * time.Second)
}

// processPending applies all pending new-member grants that are due.
func (s *InviteScheduler) processPending() {
	applied, err := s.store.ProcessPendingGrants()
	if err != nil {
		log.Printf("invite scheduler: pending grant error: %v", err)
	} else if applied > 0 {
		log.Printf("invite scheduler: applied %d pending grant(s)", applied)
	}
}
