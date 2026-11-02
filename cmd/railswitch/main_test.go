package main

import (
	"os"
	"slices"
	"syscall"
	"testing"
)

// TestStopSignals: a hang-up (a closed terminal or dashboard panel) cancels
// like SIGTERM, so railswitch stops the commands it started instead of
// dying with them still running.
func TestStopSignals(t *testing.T) {
	for _, s := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !slices.Contains(stopSignals, os.Signal(s)) {
			t.Errorf("stopSignals lacks %v", s)
		}
	}
}
