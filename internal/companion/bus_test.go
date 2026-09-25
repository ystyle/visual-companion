package companion

import (
	"sync"
	"testing"
	"time"
)

func TestBroadcastReachesEverySubscriber(t *testing.T) {
	b := newBus()
	a, cancelA := b.subscribe()
	c, cancelC := b.subscribe()
	defer cancelA()
	defer cancelC()

	b.broadcast(busMsg{Type: "reload", Generation: 7})

	for i, ch := range []<-chan busMsg{a, c} {
		select {
		case msg := <-ch:
			if msg.Type != "reload" || msg.Generation != 7 {
				t.Errorf("subscriber %d got %+v", i, msg)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d never received the broadcast", i)
		}
	}
}

func TestCancelStopsDeliveryAndClosesChannel(t *testing.T) {
	b := newBus()
	ch, cancel := b.subscribe()
	cancel()

	if _, open := <-ch; open {
		t.Error("channel should be closed after cancel")
	}
	if n := b.subscriberCount(); n != 0 {
		t.Errorf("subscriberCount = %d, want 0", n)
	}

	// A late broadcast must not panic on the closed channel.
	b.broadcast(busMsg{Type: "reload"})
}

func TestCancelIsIdempotent(t *testing.T) {
	b := newBus()
	_, cancel := b.subscribe()
	cancel()
	cancel() // must not panic on double close
	if n := b.subscriberCount(); n != 0 {
		t.Errorf("subscriberCount = %d, want 0", n)
	}
}

func TestCloseAllReleasesEverySubscriber(t *testing.T) {
	b := newBus()
	chans := make([]<-chan busMsg, 0, 3)
	for i := 0; i < 3; i++ {
		ch, _ := b.subscribe()
		chans = append(chans, ch)
	}

	b.closeAll()

	for i, ch := range chans {
		select {
		case _, open := <-ch:
			if open {
				t.Errorf("subscriber %d channel still open after closeAll", i)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d channel was never closed", i)
		}
	}
	// Subscribing after shutdown yields an already-closed channel rather than
	// blocking forever.
	late, cancel := b.subscribe()
	defer cancel()
	if _, open := <-late; open {
		t.Error("late subscriber should get a closed channel")
	}
}

func TestSlowSubscriberDoesNotBlockBroadcast(t *testing.T) {
	b := newBus()
	slow, cancelSlow := b.subscribe()
	defer cancelSlow()
	fast, cancelFast := b.subscribe()
	defer cancelFast()

	// Overflow the slow subscriber's buffer.
	for i := 0; i < 10; i++ {
		b.broadcast(busMsg{Type: "reload", Generation: i})
	}

	done := make(chan struct{})
	go func() {
		b.broadcast(busMsg{Type: "reload", Generation: 99})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a slow subscriber blocked the broadcaster")
	}

	// The fast one still has a usable stream.
	select {
	case <-fast:
	case <-time.After(time.Second):
		t.Fatal("fast subscriber received nothing")
	}
	_ = slow
}

func TestSubscriberCountTracksConnections(t *testing.T) {
	b := newBus()
	if n := b.subscriberCount(); n != 0 {
		t.Fatalf("fresh bus has %d subscribers", n)
	}
	_, c1 := b.subscribe()
	_, c2 := b.subscribe()
	if n := b.subscriberCount(); n != 2 {
		t.Fatalf("subscriberCount = %d, want 2", n)
	}
	c1()
	if n := b.subscriberCount(); n != 1 {
		t.Fatalf("after cancel, subscriberCount = %d, want 1", n)
	}
	c2()
	if n := b.subscriberCount(); n != 0 {
		t.Fatalf("after both cancel, subscriberCount = %d, want 0", n)
	}
}

func TestConcurrentSubscribeAndBroadcastIsRace(t *testing.T) {
	b := newBus()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := b.subscribe()
			defer cancel()
			select {
			case <-ch:
			case <-time.After(50 * time.Millisecond):
			}
		}()
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b.broadcast(busMsg{Type: "reload", Generation: n})
		}(i)
	}
	wg.Wait()
}
