package clock

import (
	"sync"
	"testing"
	"time"
)

// compiler proof that confirms System and *Fake have Now() time.Time
var (
	_ Clock = System{}
	_ Clock = (*Fake)(nil)
)

func TestFakeSetAndAdvance(t *testing.T) {
	start := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	f := NewFake(start)
	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %s, want %s", got, start)
	}

	f.Advance(30 * time.Second)
	if got, want := f.Now(), start.Add(30*time.Second); !got.Equal(want) {
		t.Fatalf("after Advance, Now() = %s, want %s", got, want)
	}

	f.Advance(-10 * time.Second)
	if got, want := f.Now(), start.Add(20*time.Second); !got.Equal(want) {
		t.Fatalf("after negative Advance, Now() = %s, want %s", got, want)
	}

	next := start.Add(time.Hour)
	f.Set(next)
	if got := f.Now(); !got.Equal(next) {
		t.Fatalf("after Set, Now() = %s, want %s", got, next)
	}
}

func TestFakeNowRacesWithAdvance(t *testing.T) {
	start := time.Unix(1_700_000_000, 0).UTC()
	f := NewFake(start)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			f.Advance(time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = f.Now()
		}
	}()
	wg.Wait()

	want := start.Add(1000 * time.Millisecond)
	if got := f.Now(); !got.Equal(want) {
		t.Fatalf("Now() = %s, want %s", got, want)
	}
}

func TestSystemTracksWallClock(t *testing.T) {
	before := time.Now()
	got := (System{}).Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("System.Now() = %s, want a time from %s to %s", got, before, after)
	}
}
