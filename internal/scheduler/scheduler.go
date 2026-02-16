package scheduler

import (
	"container/heap"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// entry represents a scheduled job in the heap.
type entry struct {
	jobName  string
	schedule cron.Schedule
	nextRun  time.Time
}

// entryHeap is a min-heap of entries ordered by nextRun (earliest first).
type entryHeap []entry

func (h entryHeap) Len() int            { return len(h) }
func (h entryHeap) Less(i, j int) bool   { return h[i].nextRun.Before(h[j].nextRun) }
func (h entryHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x any)          { *h = append(*h, x.(entry)) }
func (h *entryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Scheduler manages job scheduling using a min-heap and a single timer goroutine.
type Scheduler struct {
	mu    sync.Mutex
	heap  entryHeap
	timer *time.Timer
	done  chan struct{}
	wg    sync.WaitGroup
	fire  func(jobName string)
	reset chan struct{} // signals the goroutine to re-read the timer
}

// NewScheduler creates a Scheduler that calls fire when a job is due.
func NewScheduler(fire func(jobName string)) *Scheduler {
	return &Scheduler{
		fire:  fire,
		done:  make(chan struct{}),
		reset: make(chan struct{}, 1),
	}
}

// AddJob adds a job with the given schedule. If the job already exists it is
// replaced. The timer is reset if the new job is the earliest.
func (s *Scheduler) AddJob(name string, schedule cron.Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing entry with the same name.
	s.removeLockedByName(name)

	e := entry{
		jobName:  name,
		schedule: schedule,
		nextRun:  NextTime(schedule, time.Now()),
	}
	heap.Push(&s.heap, e)
	s.resetTimerLocked()
}

// RemoveJob removes a job by name.
func (s *Scheduler) RemoveJob(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLockedByName(name)
	s.resetTimerLocked()
}

// removeLockedByName removes the first entry matching name. Caller must hold s.mu.
func (s *Scheduler) removeLockedByName(name string) {
	for i, e := range s.heap {
		if e.jobName == name {
			heap.Remove(&s.heap, i)
			return
		}
	}
}

// NextRunTime returns the next scheduled run time for the named job.
func (s *Scheduler) NextRunTime(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.heap {
		if e.jobName == name {
			return e.nextRun, true
		}
	}
	return time.Time{}, false
}

// Start launches the scheduler goroutine.
func (s *Scheduler) Start() {
	s.mu.Lock()
	// Create a stopped timer; it will be set properly by resetTimerLocked.
	s.timer = time.NewTimer(0)
	if !s.timer.Stop() {
		<-s.timer.C
	}
	s.resetTimerLocked()
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run()
}

// Stop signals the scheduler goroutine to exit and waits for it.
func (s *Scheduler) Stop() {
	close(s.done)
	s.wg.Wait()
}

// run is the main scheduler loop.
func (s *Scheduler) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			s.mu.Lock()
			s.timer.Stop()
			s.mu.Unlock()
			return
		case <-s.reset:
			// Timer was reset externally (AddJob/RemoveJob); loop back to
			// wait on the updated timer.
			continue
		case <-s.timer.C:
			s.mu.Lock()
			if s.heap.Len() == 0 {
				s.mu.Unlock()
				continue
			}

			now := time.Now()
			e := s.heap[0]

			if e.nextRun.After(now) {
				// Spurious wake; reset and wait again.
				s.resetTimerLocked()
				s.mu.Unlock()
				continue
			}

			// Pop the entry, fire the callback, recalculate, and re-push.
			heap.Pop(&s.heap)
			jobName := e.jobName
			e.nextRun = NextTime(e.schedule, now)
			heap.Push(&s.heap, e)
			s.resetTimerLocked()
			s.mu.Unlock()

			s.fire(jobName)
		}
	}
}

// resetTimerLocked resets the timer to fire at the earliest entry's nextRun.
// Caller must hold s.mu. Safe to call before Start (timer may be nil).
func (s *Scheduler) resetTimerLocked() {
	if s.timer == nil {
		return
	}
	s.timer.Stop()
	if s.heap.Len() == 0 {
		return
	}
	d := time.Until(s.heap[0].nextRun)
	if d < 0 {
		d = 0
	}
	s.timer.Reset(d)

	// Non-blocking send to wake the goroutine so it re-selects on the new timer.
	select {
	case s.reset <- struct{}{}:
	default:
	}
}
