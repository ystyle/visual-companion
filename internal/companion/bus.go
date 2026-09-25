package companion

import "sync"

// busMsg is a server-to-browser notification. The only kind we send is
// "reload": a new screen is ready, refresh the page.
type busMsg struct {
	Type       string `json:"type"`
	Generation int    `json:"generation,omitempty"`
}

// bus fans one message out to every connected SSE subscriber.
//
// The original implementation hand-rolled RFC 6455 for this. WebSocket was
// doing exactly one job — a single "reload" broadcast — so SSE does the same
// work with net/http and no frame codec, and EventSource reconnects on its
// own.
type bus struct {
	mu     sync.Mutex
	subs   map[chan busMsg]struct{}
	closed bool
}

func newBus() *bus {
	return &bus{subs: map[chan busMsg]struct{}{}}
}

// subscribe registers a listener. The returned cancel func must be called
// (typically via defer) when the request ends.
func (b *bus) subscribe() (<-chan busMsg, func()) {
	ch := make(chan busMsg, 4)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
	return ch, cancel
}

// broadcast delivers msg to every subscriber. A subscriber that cannot keep
// up loses the message rather than blocking the sender — a dropped reload is
// recovered by the next one, and the browser also reloads on page focus.
func (b *bus) broadcast(msg busMsg) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// closeAll terminates every subscriber. Called on session shutdown so SSE
// handlers return instead of blocking http.Server.Close.
func (b *bus) closeAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
}

// subscriberCount reports how many browsers are connected. Used to decide
// whether auto-opening a browser is still necessary.
func (b *bus) subscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
