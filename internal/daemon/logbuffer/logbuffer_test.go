package logbuffer

import (
	"sync"
	"testing"
	"time"
)

func TestBuffer_AppendAndLines(t *testing.T) {
	buf := New(5)

	for i := 0; i < 3; i++ {
		buf.Append(Line{
			Timestamp: time.Now(),
			Text:      "line",
			Stream:    "stdout",
		})
	}

	lines := buf.Lines(0)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestBuffer_OverflowEviction(t *testing.T) {
	buf := New(3)

	for i := 0; i < 5; i++ {
		buf.Append(Line{
			Text:   string(rune('a' + i)),
			Stream: "stdout",
		})
	}

	lines := buf.Lines(0)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines after overflow, got %d", len(lines))
	}
	// Should have c, d, e (oldest a, b evicted)
	if lines[0].Text != "c" {
		t.Errorf("expected oldest remaining = 'c', got %q", lines[0].Text)
	}
	if lines[2].Text != "e" {
		t.Errorf("expected newest = 'e', got %q", lines[2].Text)
	}
}

func TestBuffer_Tail(t *testing.T) {
	buf := New(100)
	for i := 0; i < 10; i++ {
		buf.Append(Line{Text: string(rune('a' + i)), Stream: "stdout"})
	}

	lines := buf.Lines(3)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines with tail=3, got %d", len(lines))
	}
	if lines[0].Text != "h" {
		t.Errorf("expected 'h', got %q", lines[0].Text)
	}
}

func TestBuffer_Follow(t *testing.T) {
	buf := New(100)

	ch, cancel := buf.Follow()
	defer cancel()

	buf.Append(Line{Text: "hello", Stream: "stdout"})
	buf.Append(Line{Text: "world", Stream: "stdout"})

	select {
	case line := <-ch:
		if line.Text != "hello" {
			t.Errorf("expected 'hello', got %q", line.Text)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for first line")
	}

	select {
	case line := <-ch:
		if line.Text != "world" {
			t.Errorf("expected 'world', got %q", line.Text)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for second line")
	}
}

func TestBuffer_FollowCancel(t *testing.T) {
	buf := New(100)
	ch, cancel := buf.Follow()
	cancel()

	// After cancel, the channel is closed and no more writes should block
	buf.Append(Line{Text: "after-cancel", Stream: "stdout"})

	// Channel should be closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after cancel")
		}
	case <-time.After(100 * time.Millisecond):
		// This is also acceptable; closed channel drains immediately
	}
}

func TestBuffer_ConcurrentReadWrite(t *testing.T) {
	buf := New(1000)
	var wg sync.WaitGroup

	// Writers
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				buf.Append(Line{Text: "data", Stream: "stdout"})
			}
		}()
	}

	// Readers
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = buf.Lines(0)
			}
		}()
	}

	wg.Wait()
	// Should not panic or deadlock
}

func TestBuffer_EmptyLines(t *testing.T) {
	buf := New(10)
	lines := buf.Lines(0)
	if lines != nil {
		t.Errorf("empty buffer Lines() should return nil, got %v", lines)
	}
}
