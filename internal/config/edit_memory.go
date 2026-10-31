package config

import (
	"fmt"
	"slices"
)

// parseMemory splits data and checks that [memory] is the shape the memory
// edits work on: one [memory] table with a guidance key (D136).
func parseMemory(data string) (*doc, bool, error) {
	d, err := parseDoc(data)
	if err != nil {
		return nil, false, fmt.Errorf("project.toml: %w", err)
	}
	has := false
	for _, s := range d.stmts {
		if len(s.path) == 0 || s.path[0] != "memory" {
			continue
		}
		switch {
		case s.header && len(s.path) == 1 && !s.array:
			has = true
		case !s.header && slices.Equal(s.table, []string{"memory"}) && slices.Equal(s.key, []string{"guidance"}):
		default:
			return nil, false, &BlockedError{d.line(s.start), "memory is not one [memory] table with a guidance key"}
		}
	}
	return d, has, nil
}

// SetMemory turns shared agent memory on (an empty [memory] table, unless
// there is one) or off (the table and its keys go).
func SetMemory(data string, on bool) (string, error) {
	d, has, err := parseMemory(data)
	if err != nil {
		return "", err
	}
	if on {
		if has {
			return data, nil
		}
		return d.appendTable([]string{"memory"}, ""), nil
	}
	// Last first, so each statement's offsets still hold; the header alone
	// first would hand its keys to the table before it.
	out := data
	for i := len(d.stmts) - 1; i >= 0; i-- {
		if s := d.stmts[i]; len(s.path) > 0 && s.path[0] == "memory" {
			out = out[:s.start] + out[s.end:]
		}
	}
	return out, nil
}

// SetMemoryGuidance sets [memory] guidance, turning memory on; "" removes
// it (the default guidance).
func SetMemoryGuidance(data, guidance string) (string, error) {
	if len(guidance) > maxGuidance {
		return "", fmt.Errorf("memory.guidance: %d bytes is over %d", len(guidance), maxGuidance)
	}
	d, _, err := parseMemory(data)
	if err != nil {
		return "", err
	}
	s := d.find([]string{"memory"}, "guidance")
	switch {
	case s != nil && guidance == "":
		return d.removeStmt(s), nil
	case s != nil:
		return d.setValue(s, QuoteString(guidance)), nil
	case guidance == "":
		return SetMemory(data, true)
	}
	return d.insertKey([]string{"memory"}, "guidance", QuoteString(guidance)), nil
}
