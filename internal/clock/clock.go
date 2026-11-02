package clock

import (
	"sync"
	"time"
)

// reads the current time
type Clock interface {
	Now() time.Time
}

// reads the wall clock
type System struct{}

// returns time.Now
func (System) Now() time.Time { return time.Now() }

// clock whose time changes only when a test says so
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// returns a Fake set to start
func NewFake(start time.Time) *Fake {
	return &Fake{now: start}
}

// reports the time last stored
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// adds d to the stored time
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// stores t as the current time
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}
