// Package unithash stamps a rendered .container unit with a label holding
// a hash of its own content, so `up` can tell a container created from an
// older unit (its label differs) without relying on systemd's
// NeedDaemonReload, which any daemon-reload clears.
package unithash

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// Key is the label key for a project prefix ("railswitch" for the
// machine's Traefik).
func Key(prefix string) string { return prefix + ".unit-hash" }

// Sum is the first 12 hex of the sha256 of unit followed by each input,
// every input length-prefixed so that no two input lists hash alike. With
// no input it is the sha256 of unit alone.
func Sum(unit []byte, inputs ...[]byte) string {
	h := sha256.New()
	h.Write(unit)
	for _, in := range inputs {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(in)))
		h.Write(n[:])
		h.Write(in)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

const section = "[Container]\n"

// Stamp returns unit with Label=<key>=<Sum(unit, inputs…)> as the first line
// of its [Container] section; the hash is of the unit without that line plus
// the inputs the container is also created from (its env file).
func Stamp(unit []byte, key string, inputs ...[]byte) []byte {
	i := bytes.Index(unit, []byte("\n"+section))
	if i < 0 {
		return unit
	}
	i += 1 + len(section)
	label := "Label=" + key + "=" + Sum(unit, inputs...) + "\n"
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
