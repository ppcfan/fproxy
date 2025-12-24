package server

import (
	"testing"
)

func TestDedupWindow_IsDuplicate(t *testing.T) {
	t.Run("first packet is not duplicate", func(t *testing.T) {
		d := NewDedupWindow(100)
		if d.IsDuplicate(1) {
			t.Error("first packet should not be duplicate")
		}
	})

	t.Run("same sequence is duplicate", func(t *testing.T) {
		d := NewDedupWindow(100)
		d.IsDuplicate(1)
		if !d.IsDuplicate(1) {
			t.Error("same sequence should be duplicate")
		}
	})

	t.Run("different sequences not duplicate", func(t *testing.T) {
		d := NewDedupWindow(100)
		if d.IsDuplicate(1) {
			t.Error("seq 1 should not be duplicate")
		}
		if d.IsDuplicate(2) {
			t.Error("seq 2 should not be duplicate")
		}
		if d.IsDuplicate(3) {
			t.Error("seq 3 should not be duplicate")
		}
	})

	t.Run("out of order packets", func(t *testing.T) {
		d := NewDedupWindow(100)
		d.IsDuplicate(1)
		d.IsDuplicate(3)
		if d.IsDuplicate(2) {
			t.Error("out of order seq 2 should not be duplicate")
		}
		// But repeated should be
		if !d.IsDuplicate(2) {
			t.Error("repeated seq 2 should be duplicate")
		}
	})

	t.Run("window slides and old entries removed", func(t *testing.T) {
		windowSize := 10
		d := NewDedupWindow(windowSize)

		// Add packets 0-9
		for i := uint32(0); i < 10; i++ {
			if d.IsDuplicate(i) {
				t.Errorf("seq %d should not be duplicate", i)
			}
		}

		// Add more packets to slide window past seq 0
		for i := uint32(10); i < 20; i++ {
			if d.IsDuplicate(i) {
				t.Errorf("seq %d should not be duplicate", i)
			}
		}

		// Very old sequence should be treated as duplicate
		if !d.IsDuplicate(0) {
			t.Error("very old seq 0 should be treated as duplicate (outside window)")
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		d := NewDedupWindow(1000)
		done := make(chan bool)

		// Spawn multiple goroutines checking sequences
		for g := 0; g < 10; g++ {
			go func(offset uint32) {
				for i := uint32(0); i < 100; i++ {
					d.IsDuplicate(offset*100 + i)
				}
				done <- true
			}(uint32(g))
		}

		// Wait for all to complete
		for g := 0; g < 10; g++ {
			<-done
		}
	})

	t.Run("zero sequence number", func(t *testing.T) {
		d := NewDedupWindow(100)
		if d.IsDuplicate(0) {
			t.Error("first seq 0 should not be duplicate")
		}
		if !d.IsDuplicate(0) {
			t.Error("repeated seq 0 should be duplicate")
		}
	})
}
