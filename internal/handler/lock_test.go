package handler

import (
	"testing"
	"time"
)

func TestLockSheetSerializesSameSheet(t *testing.T) {
	h := New(nil, "", nil)
	unlock := h.lockSheet("sheet-a")

	acquired := make(chan struct{})
	go func() {
		defer h.lockSheet("sheet-a")()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second command on the same sheet ran while the first held the lock")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second command never ran after the first released the lock")
	}
}

func TestLockSheetDoesNotBlockOtherSheets(t *testing.T) {
	h := New(nil, "", nil)
	defer h.lockSheet("sheet-a")()

	acquired := make(chan struct{})
	go func() {
		defer h.lockSheet("sheet-b")()
		close(acquired)
	}()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("a command on sheet-b waited behind sheet-a")
	}
}
