// Package agentqueue is a worktree's agent work queue (D159): prompts kept
// in order on the host, in <ws>/.railswitch/queue/<slug>/ (no container
// mounts it), typed into the worktree's agent one at a time as it goes idle.
package agentqueue

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// Limits: an item is at most what agent send types at once; a queue holds
// at most MaxItems.
const (
	MaxText  = 4096
	MaxItems = 20
	maxFile  = 1 << 20
)

// Item is one queued prompt.
type Item struct {
	ID    int    `json:"id"`
	Text  string `json:"text"`
	Added int64  `json:"added"` // Unix seconds
}

// Queue is a worktree's queue file.
type Queue struct {
	Schema int `json:"schema"`
	Next   int `json:"next"` // the ID the next item gets
	// Delivered is when the last delivery started (Unix seconds): the
	// agent's status must be at least as new before the next one.
	Delivered int64  `json:"delivered"`
	Items     []Item `json:"items"`
}

// Dir is worktree slug's queue directory.
func Dir(ws, slug string) string { return filepath.Join(ws, ".railswitch", "queue", slug) }

func file(ws, slug string) string { return filepath.Join(Dir(ws, slug), "queue.json") }

// Read is worktree slug's queue; an empty one when it has none.
func Read(ws, slug string) (*Queue, error) {
	data, err := safefile.Read(file(ws, slug), maxFile)
	if errors.Is(err, fs.ErrNotExist) {
		return &Queue{Schema: 1, Next: 1, Items: []Item{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var q Queue
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("agent queue: %s: %w", file(ws, slug), err)
	}
	if q.Schema > 1 {
		return nil, fmt.Errorf("agent queue: %s was written by a newer railswitch (schema %d)", file(ws, slug), q.Schema)
	}
	if q.Items == nil {
		q.Items = []Item{}
	}
	return &q, nil
}

// Update applies fn to worktree slug's queue under its lock and writes it
// (0600, atomically) when fn succeeds.
func Update(ws, slug string, fn func(*Queue) error) error {
	unlock, ok, err := lock(filepath.Join(Dir(ws, slug), "lock"), false)
	if err != nil || !ok {
		return err
	}
	defer unlock()
	q, err := Read(ws, slug)
	if err != nil {
		return err
	}
	if err := fn(q); err != nil {
		return err
	}
	q.Schema = 1
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return tomlfile.WriteAtomic(file(ws, slug), data, 0o600)
}

// Add appends text and returns its item.
func Add(ws, slug, text string, now time.Time) (Item, error) {
	if len(text) > MaxText {
		return Item{}, fmt.Errorf("agent queue: more than %d bytes", MaxText)
	}
	var it Item
	err := Update(ws, slug, func(q *Queue) error {
		if len(q.Items) >= MaxItems {
			return fmt.Errorf("agent queue: %s holds %d items already", slug, MaxItems)
		}
		it = Item{ID: max(q.Next, 1), Text: text, Added: now.Unix()}
		q.Next = it.ID + 1
		q.Items = append(q.Items, it)
		return nil
	})
	return it, err
}

// Remove drops item id; a missing one is an error unless quiet.
func Remove(q *Queue, id int, quiet bool) error {
	i := slices.IndexFunc(q.Items, func(it Item) bool { return it.ID == id })
	if i < 0 {
		if quiet {
			return nil
		}
		return fmt.Errorf("agent queue: no item %d", id)
	}
	q.Items = slices.Delete(q.Items, i, i+1)
	return nil
}

// TryDeliver takes worktree slug's delivery lock without waiting: ok false
// while another delivery holds it.
func TryDeliver(ws, slug string) (unlock func(), ok bool, err error) {
	return lock(filepath.Join(Dir(ws, slug), "deliver.lock"), true)
}

// lock flocks path (created 0600, its directory 0700), waiting unless nb.
func lock(path string, nb bool) (unlock func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_EX
	if nb {
		how |= syscall.LOCK_NB
	}
	for {
		if err = syscall.Flock(int(f.Fd()), how); err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { f.Close() }, true, nil
}
