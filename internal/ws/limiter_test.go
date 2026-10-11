package ws

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestClientLimiterRejectsAtCapacity(t *testing.T) {
	l := NewClientLimiter(2)
	if !l.Acquire() || !l.Acquire() {
		t.Fatal("first two acquires must succeed")
	}
	if l.Acquire() {
		t.Fatal("acquire past cap must fail")
	}
	if l.Active() != 2 {
		t.Fatalf("active = %d, want 2", l.Active())
	}
	l.Release()
	if !l.Acquire() {
		t.Fatal("acquire after release must succeed")
	}
}

func TestClientLimiterUnlimited(t *testing.T) {
	for _, max := range []int{0, -1} {
		l := NewClientLimiter(max)
		if !l.Acquire() {
			t.Fatalf("max=%d: acquire must always succeed", max)
		}
		if l.Active() != 0 {
			t.Fatalf("max=%d: unlimited mode must not count", max)
		}
		l.Release() // must be a no-op, not a panic
	}
	if l := NewClientLimiter(0); l.Max() != 0 {
		t.Fatalf("max = %d, want 0 (unlimited)", l.Max())
	}
}

func TestClientLimiterConcurrentHammerExact(t *testing.T) {
	const cap, workers, attempts = 8, 32, 200
	l := NewClientLimiter(cap)
	var wg sync.WaitGroup
	var admitted, rejected atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				if l.Acquire() {
					admitted.Add(1)
					l.Release()
				} else {
					rejected.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if got := l.Active(); got != 0 {
		t.Fatalf("active = %d after all releases, want 0", got)
	}
	if total := admitted.Load() + rejected.Load(); total != workers*attempts {
		t.Fatalf("attempts = %d, want %d", total, workers*attempts)
	}
	if l.Max() != cap {
		t.Fatalf("max = %d, want %d", l.Max(), cap)
	}
}

func TestClientLimiterHeldSlotsExactUnderContention(t *testing.T) {
	// Acquire/release waves under contention: at no point may the
	// active count exceed the cap (a rejected acquire leaves the
	// count untouched).
	const cap = 4
	l := NewClientLimiter(cap)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if !l.Acquire() {
					// Refusal is legitimate under contention; the
					// cap itself is what must never be crossed.
					if a := l.Active(); a > cap {
						t.Errorf("active = %d exceeds cap %d on refusal", a, cap)
					}
					continue
				}
				if a := l.Active(); a > cap {
					t.Errorf("active = %d exceeds cap %d while held", a, cap)
				}
				l.Release()
			}
		}()
	}
	wg.Wait()
	if got := l.Active(); got != 0 {
		t.Fatalf("active = %d after all releases, want 0", got)
	}
}

