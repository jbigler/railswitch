// Package agentstatus reads an agent's status file (D58): the JSON an
// agent's hook writes into its worktree's status dir, which the agent's
// container can write, so every read treats it as hostile.
package agentstatus

import (
	"encoding/json"
	"regexp"
	"slices"

	"github.com/jbigler/railswitch/internal/safefile"
)

// Status is one status file: {"v":1,"state":"…","message":"…","ts":<int>},
// optionally "usage" (the agent's current session's tokens).
type Status struct {
	V       int    `json:"v"`
	State   string `json:"state"`
	Message string `json:"message"`
	TS      int64  `json:"ts"`
	Usage   *Usage `json:"usage,omitempty"`
}

// Usage is the tokens of the agent's current session, as its package's hook
// counts them: input, output, and input read from or written to the cache;
// Cost is the session's cost in US dollars when the package reports one
// (D162), nil otherwise.
type Usage struct {
	Input      int64    `json:"input"`
	Output     int64    `json:"output"`
	CacheRead  int64    `json:"cache_read"`
	CacheWrite int64    `json:"cache_write"`
	Cost       *float64 `json:"cost,omitempty"`
}

// maxTokens bounds each count, maxCost the cost: a larger or negative one
// is no usage.
const (
	maxTokens = 1e13
	maxCost   = 1e6
)

func (u *Usage) valid() bool {
	for _, n := range []int64{u.Input, u.Output, u.CacheRead, u.CacheWrite} {
		if n < 0 || n > maxTokens {
			return false
		}
	}
	return u.Cost == nil || *u.Cost >= 0 && *u.Cost <= maxCost
}

// maxSize is the largest status file read; a larger one is off.
const maxSize = 65536

// states are the states of the contract (D58).
var states = []string{"permission", "question", "working", "done", "idle", "off"}

// Read reads the status file at path. A missing, refused (a symlink, a
// FIFO, oversized), invalid file, one with v != 1 or an unknown state is
// state off; an invalid usage is dropped alone. Read never fails.
func Read(path string) Status {
	off := Status{V: 1, State: "off"}
	data, err := safefile.Read(path, maxSize)
	if err != nil {
		return off
	}
	// usage is read on its own: a bad one (not an object, a fraction, a
	// number out of range) must not hide the state.
	var raw struct {
		Status
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.V != 1 || !slices.Contains(states, raw.State) {
		return off
	}
	s := raw.Status
	var u Usage
	if len(raw.Usage) > 0 && string(raw.Usage) != "null" && json.Unmarshal(raw.Usage, &u) == nil && u.valid() {
		s.Usage = &u
	}
	return s
}

// replyRe is the old dashboard's "waiting for a reply" rule: a message
// ending in a question mark, maybe followed by closing punctuation.
var replyRe = regexp.MustCompile("\\?[\\s)\\]\"'*_`”’]*\\z")

// Reply reports a finished turn that ends in a question to the user.
func (s Status) Reply() bool {
	return s.State == "done" && replyRe.MatchString(s.Message)
}
