package messaging

import (
	"sync"
	"testing"
	"time"
)

// Work for one chat runs in submission order, one job at a time.
func TestChatQueueOrderWithinChat(t *testing.T) {
	q := NewChatQueue()
	var (
		mu      sync.Mutex
		got     []int
		running int
		wg      sync.WaitGroup
	)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		q.Submit("chat", func() {
			defer wg.Done()
			mu.Lock()
			running++
			if running > 1 {
				t.Error("two jobs for the same chat ran at once")
			}
			mu.Unlock()

			time.Sleep(time.Millisecond)

			mu.Lock()
			got = append(got, i)
			running--
			mu.Unlock()
		})
	}
	wg.Wait()

	for i, v := range got {
		if v != i {
			t.Fatalf("job order = %v, want 0..49 in order", got)
		}
	}
}

// A stalled chat must not hold up another chat — the bug this queue fixes.
func TestChatQueueSlowChatDoesNotBlockOthers(t *testing.T) {
	q := NewChatQueue()
	release := make(chan struct{})
	defer close(release)

	q.Submit("slow", func() { <-release })

	done := make(chan struct{})
	q.Submit("fast", func() { close(done) })

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a stalled chat blocked work for a different chat")
	}
}

// A chat's worker exits when its queue drains, and the chat can be reused.
func TestChatQueueDrainsAndRestarts(t *testing.T) {
	q := NewChatQueue()
	for round := 0; round < 3; round++ {
		done := make(chan struct{})
		q.Submit("chat", func() { close(done) })
		<-done

		deadline := time.Now().Add(2 * time.Second)
		for {
			q.mu.Lock()
			_, running := q.pending["chat"]
			q.mu.Unlock()
			if !running {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("worker did not exit after its queue drained")
			}
			time.Sleep(time.Millisecond)
		}
	}
}
