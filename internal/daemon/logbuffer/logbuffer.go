// Package logbuffer implements a per-session ring buffer for agent output.
// It supports concurrent reads and writes, and a follow-mode channel for
// live streaming.
package logbuffer

import (
	"sync"
	"time"
)

const DefaultSize = 10000

// Line represents a single log line with metadata.
type Line struct {
	Timestamp time.Time `json:"timestamp"`
	Text      string    `json:"line"`
	Stream    string    `json:"stream"` // "stdout" or "stderr"
}

// Buffer is a thread-safe ring buffer for log lines.
type Buffer struct {
	mu        sync.RWMutex
	lines     []Line
	size      int
	head      int // index of the oldest entry (when full)
	count     int // number of entries stored
	followers []*follower
}

type follower struct {
	ch   chan Line
	done chan struct{}
}

// New creates a new Buffer with the given maximum size.
func New(size int) *Buffer {
	if size <= 0 {
		size = DefaultSize
	}
	return &Buffer{
		lines: make([]Line, size),
		size:  size,
	}
}

// Append adds a line to the buffer and delivers it to all active followers.
func (b *Buffer) Append(line Line) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count < b.size {
		b.lines[b.count] = line
		b.count++
	} else {
		// Overwrite oldest (ring buffer behaviour)
		b.lines[b.head] = line
		b.head = (b.head + 1) % b.size
	}

	// Deliver to followers (non-blocking)
	for _, f := range b.followers {
		select {
		case f.ch <- line:
		default:
			// Follower is slow; drop the line rather than blocking
		}
	}
}

// Lines returns all buffered lines in order from oldest to newest.
// If tail > 0, only the last 'tail' lines are returned.
func (b *Buffer) Lines(tail int) []Line {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count == 0 {
		return nil
	}

	result := make([]Line, b.count)
	for i := 0; i < b.count; i++ {
		result[i] = b.lines[(b.head+i)%b.size]
	}

	if tail > 0 && tail < len(result) {
		result = result[len(result)-tail:]
	}
	return result
}

// Follow registers a new follower that receives lines as they are appended.
// The returned channel will receive new lines. Call close() on the returned
// cancel function to stop following.
func (b *Buffer) Follow() (<-chan Line, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	f := &follower{
		ch:   make(chan Line, 256),
		done: make(chan struct{}),
	}
	b.followers = append(b.followers, f)

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		newFollowers := b.followers[:0]
		for _, existing := range b.followers {
			if existing != f {
				newFollowers = append(newFollowers, existing)
			}
		}
		b.followers = newFollowers
		close(f.ch)
	}

	return f.ch, cancel
}

// Len returns the current number of buffered lines.
func (b *Buffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}
