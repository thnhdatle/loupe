// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"testing"
	"time"

	"github.com/thnhdatle/loupe/internal/platform/clock"
)

var start = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func TestFake_Now_AdvancesOnlyOnAdvance(t *testing.T) {
	t.Parallel()
	f := clock.NewFake(start)
	if !f.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", f.Now(), start)
	}
	f.Advance(90 * time.Second)
	if want := start.Add(90 * time.Second); !f.Now().Equal(want) {
		t.Fatalf("Now = %v, want %v", f.Now(), want)
	}
}

func TestFake_After_FiresWhenDue(t *testing.T) {
	t.Parallel()
	f := clock.NewFake(start)
	ch := f.After(time.Minute)
	f.Advance(59 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}
	f.Advance(time.Second)
	select {
	case got := <-ch:
		if want := start.Add(time.Minute); !got.Equal(want) {
			t.Fatalf("fired at %v, want %v", got, want)
		}
	default:
		t.Fatal("did not fire")
	}
}

func TestFake_NewTicker_TicksAndStops(t *testing.T) {
	t.Parallel()
	f := clock.NewFake(start)
	tk := f.NewTicker(time.Second)
	f.Advance(time.Second)
	if got := <-tk.C(); !got.Equal(start.Add(time.Second)) {
		t.Fatalf("first tick at %v", got)
	}
	f.Advance(time.Second)
	if got := <-tk.C(); !got.Equal(start.Add(2 * time.Second)) {
		t.Fatalf("second tick at %v", got)
	}
	tk.Stop()
	f.Advance(time.Hour)
	select {
	case <-tk.C():
		t.Fatal("tick after Stop")
	default:
	}
}

func TestFake_NewTicker_DropsTicksWhenSlow(t *testing.T) {
	t.Parallel()
	f := clock.NewFake(start)
	tk := f.NewTicker(time.Second)
	f.Advance(5 * time.Second)
	<-tk.C()
	select {
	case <-tk.C():
		t.Fatal("expected dropped ticks")
	default:
	}
}

func TestFake_NewTicker_PanicsOnNonPositive(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	clock.NewFake(start).NewTicker(0)
}

func TestSystem_Clock_Works(t *testing.T) {
	t.Parallel()
	var c clock.Clock = clock.System{}
	if c.Now().IsZero() {
		t.Fatal("zero time")
	}
	select {
	case <-c.After(time.Millisecond):
	case <-time.After(5 * time.Second):
		t.Fatal("After did not fire")
	}
	tk := c.NewTicker(time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C():
	case <-time.After(5 * time.Second):
		t.Fatal("ticker did not fire")
	}
}
