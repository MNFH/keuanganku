package messaging

import "sync"

// ChatQueue runs work concurrently across chats but strictly in order within
// one chat.
//
// Both halves matter. Across chats: one slow message — a Gemini call stalled
// by a flaky network, a slow Sheets write — must not hold up everyone else,
// which is what a single sequential loop did. Within a chat: order is part of
// the conversation. A follow-up answer ("jago") must be handled after the
// question it answers, and two writes from the same person must land in the
// order they were sent.
//
// There is no goroutine per idle chat: a chat's worker exists only while it
// has work queued, and exits once its queue drains.
type ChatQueue struct {
	mu sync.Mutex
	// pending holds work waiting behind the running job for each chat. A key
	// being present (even with an empty slice) means a worker is running.
	pending map[string][]func()
}

// NewChatQueue returns an empty queue.
func NewChatQueue() *ChatQueue {
	return &ChatQueue{pending: make(map[string][]func())}
}

// Submit schedules fn for chatID. It never blocks: fn starts at once if the
// chat is idle, or after the chat's earlier work otherwise.
func (q *ChatQueue) Submit(chatID string, fn func()) {
	q.mu.Lock()
	if list, running := q.pending[chatID]; running {
		q.pending[chatID] = append(list, fn)
		q.mu.Unlock()
		return
	}
	q.pending[chatID] = nil
	q.mu.Unlock()

	go q.run(chatID, fn)
}

func (q *ChatQueue) run(chatID string, fn func()) {
	for {
		fn()

		q.mu.Lock()
		list := q.pending[chatID]
		if len(list) == 0 {
			delete(q.pending, chatID)
			q.mu.Unlock()
			return
		}
		fn = list[0]
		q.pending[chatID] = list[1:]
		q.mu.Unlock()
	}
}
