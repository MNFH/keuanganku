package telegram

import "sync"

// chatQueues runs each chat's jobs in arrival order on a goroutine of its own,
// so a slow reply in one chat — a Gemini parse, a run of Sheets calls — does
// not hold up every other chat behind it.
//
// Order within a chat still matters: a user who sends "/dompet tambah BCA"
// and then "/masuk 50rb BCA" expects the wallet to exist by the second.
type chatQueues struct {
	mu      sync.Mutex
	pending map[int64][]func() // present while a chat's worker is running
	wg      sync.WaitGroup
}

func newChatQueues() *chatQueues {
	return &chatQueues{pending: make(map[int64][]func())}
}

// submit queues job behind any earlier jobs for the same chat.
func (q *chatQueues) submit(chatID int64, job func()) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if jobs, running := q.pending[chatID]; running {
		q.pending[chatID] = append(jobs, job)
		return
	}
	q.pending[chatID] = nil
	q.wg.Add(1)
	go q.drain(chatID, job)
}

func (q *chatQueues) drain(chatID int64, job func()) {
	defer q.wg.Done()
	for job != nil {
		job()

		q.mu.Lock()
		if jobs := q.pending[chatID]; len(jobs) > 0 {
			job, q.pending[chatID] = jobs[0], jobs[1:]
		} else {
			delete(q.pending, chatID)
			job = nil
		}
		q.mu.Unlock()
	}
}

// wait blocks until every submitted job has finished.
func (q *chatQueues) wait() { q.wg.Wait() }
