package telegram

import (
	"sync"
	"testing"
	"time"
)

func TestChatQueuesKeepOrderWithinChat(t *testing.T) {
	q := newChatQueues()
	var mu sync.Mutex
	var got []int

	for i := 0; i < 50; i++ {
		i := i
		q.submit(1, func() {
			// Uneven work, so a queue that ran jobs in parallel would
			// almost certainly finish them out of order.
			time.Sleep(time.Duration(50-i) * 10 * time.Microsecond)
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
		})
	}
	q.wait()

	if len(got) != 50 {
		t.Fatalf("ran %d jobs, want 50", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("job order = %v, want 0..49 in order", got)
		}
	}
}

func TestChatQueuesDoNotBlockOtherChats(t *testing.T) {
	q := newChatQueues()
	release := make(chan struct{})
	done := make(chan struct{})

	q.submit(1, func() { <-release }) // chat 1 is stuck on a slow reply
	q.submit(2, func() { close(done) })

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("chat 2 waited behind chat 1's slow job")
	}
	close(release)
	q.wait()
}

func TestChatQueuesRestartAfterDraining(t *testing.T) {
	q := newChatQueues()
	ran := 0
	q.submit(1, func() { ran++ })
	q.wait()
	// The chat's worker has exited; a new message must start another.
	q.submit(1, func() { ran++ })
	q.wait()

	if ran != 2 {
		t.Fatalf("ran %d jobs, want 2", ran)
	}
}
