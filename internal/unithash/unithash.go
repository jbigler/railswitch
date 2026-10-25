// Package unithash stamps a rendered .container unit with a label holding
// a hash of its own content, so `up` can tell a container created from an
// older unit (its label differs) without relying on systemd's
// NeedDaemonReload, which any daemon-reload clears.
package unithash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

// Key is the label key for a project prefix ("railswitch" for the
// machine's Traefik).
func Key(prefix string) string { return prefix + ".unit-hash" }

// Sum is the first 12 hex of the sha256 of unit.
func Sum(unit []byte) string {
	h := sha256.Sum256(unit)
	return hex.EncodeToString(h[:])[:12]
}

const section = "[Container]\n"

// Stamp returns unit with Label=<key>=<Sum(unit)> as the first line of its
// [Container] section; the hash is of the unit without that line.
func Stamp(unit []byte, key string) []byte {
	i := bytes.Index(unit, []byte("\n"+section))
	if i < 0 {
		return unit
	}
	i += 1 + len(section)
	label := "Label=" + key + "=" + Sum(unit) + "\n"
	out := make([]byte, 0, len(unit)+len(label))
	out = append(out, unit[:i]...)
	out = append(out, label...)
	return append(out, unit[i:]...)
}

// Read returns the value of unit's Label=<key>=… line, or "".
func Read(unit []byte, key string) string {
	prefix := []byte("Label=" + key + "=")
	for line := range bytes.Lines(unit) {
		if v, ok := bytes.CutPrefix(bytes.TrimRight(line, "\n"), prefix); ok {
			return string(v)
		}
	}
	return ""
}
