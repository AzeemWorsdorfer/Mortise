// Package daemon — eventbus_test.go exercises the in-process event
// bus that fans ServerEvents out to every connected TUI client.
//
// These tests are unit-level: no socket, no handler, no Connect
// transport. The bus is observed through the public Subscribe /
// Publish / Unsubscribe / Run / Close seams, so refactoring the
// internal fan-out goroutine does not invalidate the suite.
package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// newTestEventBus returns a started EventBus for tests. The Run
// goroutine runs until ctx is canceled, at which point it exits and
// the bus is safe to assert against. Tests must call cancel().
func newTestEventBus(t *testing.T) (*EventBus, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	bus := NewEventBus()
	go bus.Run(ctx)
	t.Cleanup(func() {
		cancel()
		bus.Close()
	})
	return bus, cancel
}

// idleEvent is a small helper that mints a fresh *ServerEvent in the
// IDLE phase. Tests use it to exercise the bus without depending on
// the production Status-event construction in handler.go.
func idleEvent() *mortisev1.ServerEvent {
	return &mortisev1.ServerEvent{
		Phase: mortisev1.AgentPhase_IDLE,
	}
}

func TestSubscribe_Publish_DeliversToAll(t *testing.T) {
	t.Parallel()

	bus, _ := newTestEventBus(t)

	a := bus.Subscribe("a")
	b := bus.Subscribe("b")
	c := bus.Subscribe("c")

	bus.Publish(idleEvent())

	// Each subscriber should see the event. Use a small receive
	// helper that tolerates a brief wait, since fan-out is async
	// (Publish hands off to Run via publishCh).
	collect := func(ch <-chan *mortisev1.ServerEvent) *mortisev1.ServerEvent {
		t.Helper()
		select {
		case ev := <-ch:
			return ev
		case <-time.After(2 * time.Second):
			t.Fatal("subscriber did not receive event within 2s")
			return nil
		}
	}
	_ = collect(a)
	_ = collect(b)
	_ = collect(c)
}

func TestSubscribe_AfterPublish_NoReplay(t *testing.T) {
	t.Parallel()

	bus, _ := newTestEventBus(t)

	// Publish BEFORE the new subscriber exists. The bus must not
	// buffer or replay — a fresh subscriber gets nothing.
	bus.Publish(idleEvent())
	// Give Run time to drain publishCh so the publish is fully
	// fanned out (to zero subscribers) before we subscribe.
	time.Sleep(50 * time.Millisecond)

	ch := bus.Subscribe("late")
	select {
	case ev := <-ch:
		t.Fatalf("new subscriber should not receive past events, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: nothing arrives.
	}
}

func TestUnsubscribe_StopsDelivery(t *testing.T) {
	t.Parallel()

	bus, _ := newTestEventBus(t)

	keep := bus.Subscribe("keep")
	gone := bus.Subscribe("gone")

	bus.Publish(idleEvent())
	// Drain both channels so the unsubscribe assertion below is not
	// confused by buffered values (closing a channel with a buffered
	// value still lets the value be received; we only want to assert
	// that the second publish does NOT deliver to "gone").
	for label, ch := range map[string]<-chan *mortisev1.ServerEvent{"keep": keep, "gone": gone} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s subscriber did not receive first event", label)
		}
	}

	bus.Unsubscribe("gone")

	bus.Publish(idleEvent())
	select {
	case ev := <-keep:
		if ev == nil {
			t.Fatal("keep subscriber received nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("keep subscriber did not receive second event after gone unsubscribed")
	}

	// The closed "gone" channel should NOT produce a value to the
	// receiver (range over a closed channel returns ok=false on the
	// zero value). Reading from it with the comma-ok form is the
	// cleanest way to assert "no value arrived".
	select {
	case ev, ok := <-gone:
		if ok {
			t.Fatalf("gone subscriber should be closed, got %+v", ev)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("gone subscriber channel was not closed by Unsubscribe")
	}
}

func TestPublish_FullBuffer_Drops(t *testing.T) {
	t.Parallel()

	bus, _ := newTestEventBus(t)

	// Subscribe without reading. The buffer is cap 64; publishing 65
	// events must result in at least one drop. We publish many more
	// than 64 to be sure the buffer was full when the bus tried to
	// deliver to this subscriber.
	ch := bus.Subscribe("slow")

	before := bus.DroppedCount()

	const n = 200
	for i := 0; i < n; i++ {
		bus.Publish(idleEvent())
	}

	after := bus.DroppedCount()
	if after <= before {
		t.Fatalf("expected drops > 0 after publishing %d events to a non-reading subscriber, before=%d after=%d",
			n, before, after)
	}
	// Give the fan-out goroutine a moment to deliver everything
	// that fit in the subscriber's 64-deep buffer. Without this
	// wait the channel can briefly be empty under -race because
	// the fan-out goroutine is still chewing through publishCh.
	deadline := time.Now().Add(1 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		got = 0
	drain:
		for {
			select {
			case <-ch:
				got++
			default:
				break drain
			}
		}
		if got > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got == 0 {
		t.Error("expected at least one event in subscriber buffer, got 0")
	}
	if got > 64 {
		t.Errorf("subscriber buffer should not hold more than 64 events, got %d", got)
	}
}

func TestPublish_Concurrent_NoRace(t *testing.T) {
	t.Parallel()

	bus, _ := newTestEventBus(t)

	// 100 concurrent publishers into 10 subscribers. The race
	// detector flags any unsynchronized access; with -race this
	// fails on a missing mutex around the subscribers map or
	// publishCh send.
	const numPublishers = 100
	const perPublisher = 10
	subs := make([]<-chan *mortisev1.ServerEvent, 10)
	for i := range subs {
		subs[i] = bus.Subscribe(string(rune('a' + i)))
	}

	var wg sync.WaitGroup
	for p := 0; p < numPublishers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				bus.Publish(idleEvent())
			}
		}()
	}
	wg.Wait()

	// Drain each subscriber so Run does not block on a full buffer.
	for _, ch := range subs {
		go func(c <-chan *mortisev1.ServerEvent) {
			for range c {
			}
		}(ch)
	}
}

func TestSubscriberCount(t *testing.T) {
	t.Parallel()

	bus, _ := newTestEventBus(t)

	if got := bus.SubscriberCount(); got != 0 {
		t.Fatalf("new bus: want 0 subscribers, got %d", got)
	}

	a := bus.Subscribe("a")
	if got := bus.SubscriberCount(); got != 1 {
		t.Errorf("after 1 subscribe: want 1, got %d", got)
	}
	_ = bus.Subscribe("b")
	if got := bus.SubscriberCount(); got != 2 {
		t.Errorf("after 2 subscribes: want 2, got %d", got)
	}
	_ = a
	bus.Unsubscribe("a")
	if got := bus.SubscriberCount(); got != 1 {
		t.Errorf("after 1 unsubscribe: want 1, got %d", got)
	}
	bus.Unsubscribe("b")
	if got := bus.SubscriberCount(); got != 0 {
		t.Errorf("after all unsubscribes: want 0, got %d", got)
	}
}

func TestRun_ExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	// A bus whose Run is not yet started. Start Run, then cancel
	// the context; Run must return within a short window.
	ctx, cancel := context.WithCancel(context.Background())
	bus := NewEventBus()
	done := make(chan struct{})
	go func() {
		bus.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of context cancel")
	}
}

func TestClose_CleansUp(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bus.Run(ctx)

	a := bus.Subscribe("a")
	b := bus.Subscribe("b")

	bus.Close()

	// All subscriber channels should be closed and the map cleared.
	if got := bus.SubscriberCount(); got != 0 {
		t.Errorf("after Close: want 0 subscribers, got %d", got)
	}
	for name, ch := range map[string]<-chan *mortisev1.ServerEvent{"a": a, "b": b} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Errorf("subscriber %q: want closed channel, got a value", name)
			}
		case <-time.After(1 * time.Second):
			t.Errorf("subscriber %q: channel not closed after Close", name)
		}
	}
}

func TestPublish_AfterClose_DoesNotPanic(t *testing.T) {
	t.Parallel()

	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bus.Run(ctx)

	// Allow Run to start reading from publishCh.
	time.Sleep(20 * time.Millisecond)

	bus.Close()

	// Publish on a closed bus must not panic. The closed flag
	// returns early before any send to publishCh. Run is still
	// alive in this test (ctx is not canceled), but the bus
	// rejects publishes anyway — that's the correct behavior:
	// shutdown means no more events.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Publish after Close panicked: %v", r)
		}
	}()
	bus.Publish(idleEvent())
}

// TestUnsubscribe_DuringPublish_Resilient stresses the subscribe /
// publish / unsubscribe hot path: while N goroutines are calling
// Publish, another goroutine is subscribing and unsubscribing. The
// race detector catches any unsynchronized map iteration or channel
// send.
func TestUnsubscribe_DuringPublish_Resilient(t *testing.T) {
	t.Parallel()

	bus, _ := newTestEventBus(t)

	var wg sync.WaitGroup
	var pubCount atomic.Int64

	stop := make(chan struct{})

	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					bus.Publish(idleEvent())
					pubCount.Add(1)
				}
			}
		}()
	}

	// Subscribe/unsubscribe churn.
	for u := 0; u < 4; u++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				name := "churn-" + string(rune('A'+id)) + "-" + string(rune('0'+(i%10)))
				ch := bus.Subscribe(name)
				bus.Unsubscribe(name)
				_ = ch
			}
		}(u)
	}

	// Let the churn finish, then stop publishers.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	if pubCount.Load() == 0 {
		t.Fatal("publishers ran zero times — test setup is wrong")
	}
}
