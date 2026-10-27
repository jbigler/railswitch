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

// Status is one status file: {"v":1,"state":"…","message":"…","ts":<int>}.
type Status struct {
	V       int    `json:"v"`
	State   string `json:"state"`
	Message string `json:"message"`
	TS      int64  `json:"ts"`
}

// maxSize is the largest status file read; a larger one is off.
const maxSize = 65536

// states are the states of the contract (D58).
var states = []string{"permission", "question", "working", "done", "idle", "off"}

// Read reads the status file at path. A missing, refused (a symlink, a
// FIFO, oversized), invalid file, one with v != 1 or an unknown state is
// state off; Read never fails.
func Read(path string) Status {
	off := Status{V: 1, State: "off"}
	data, err := safefile.Read(path, maxSize)
	if err != nil {
		return off
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil || s.V != 1 || !slices.Contains(states, s.State) {
		return off
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
