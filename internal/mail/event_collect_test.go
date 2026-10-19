package mail

import (
	"sync"
	"testing"
	"time"
)

// collectEvents records events of one type while a sync runs. The bus drops
// events for a full subscriber, so it has to be drained concurrently.
func collectEvents(t *testing.T, bus *EventBus, eventType EventType) func() []Event {
	t.Helper()
	ch := bus.Subscribe()
	stop, done := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var seen []Event
	go func() {
		defer close(done)
		for {
			select {
			case e := <-ch:
				if e.Type == eventType {
					mu.Lock()
					seen = append(seen, e)
					mu.Unlock()
				}
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done; bus.Unsubscribe(ch) })
	return func() []Event {
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		got := seen
		seen = nil
		return got
	}
}
