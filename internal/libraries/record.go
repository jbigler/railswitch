// Package libraries keeps the library repositories of this machine (D103,
// D104): libraries.toml, the hardened fetch, and each library's checkout
// under <data>/libraries/<alias>/.
package libraries

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// RecordSchema is fetch.toml's schema (D39).
const RecordSchema = 1

// Record is <data>/libraries/<alias>/fetch.toml: the last fetch of a
// library and the checkout in current/ beside it.
type Record struct {
	Schema  int            `toml:"schema"`
	ID      string         `toml:"id"`      // the library's id
	Commit  string         `toml:"commit"`  // hex
	Serial  int64          `toml:"serial"`  // its index's serial
	Status  signing.Status `toml:"status"`  // the last fetch's verdict
	Fetched time.Time      `toml:"fetched"` // RFC 3339
	// Signer is the fingerprint of the key that signed current/'s index;
	// "" when there is no current/.
	Signer string `toml:"signer"`
	Reason string `toml:"reason"`
}

// Dir is the alias's directory, <data>/libraries/<alias>. alias must be
// valid (config.ValidAlias).
func Dir(d machine.Dirs, alias string) string {
	return filepath.Join(d.Data, "libraries", alias)
}

// CurrentDir is the alias's verified checkout, <data>/libraries/<alias>/current.
func CurrentDir(d machine.Dirs, alias string) string {
	return filepath.Join(Dir(d, alias), "current")
}

// ReadRecord reads the alias's fetch.toml strictly. A missing file is an
// error wrapping fs.ErrNotExist: the library was never fetched.
func ReadRecord(d machine.Dirs, alias string) (*Record, error) {
	if err := config.ValidAlias(alias); err != nil {
		return nil, err
	}
	path := filepath.Join(Dir(d, alias), "fetch.toml")
	data, err := safefile.Read(path, safefile.Limit)
	if err != nil {
		return nil, err
	}
	var r Record
	if err := tomlfile.Decode(data, RecordSchema, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != RecordSchema {
		return nil, fmt.Errorf("%s: schema = %d, want %d", path, r.Schema, RecordSchema)
	}
	return &r, nil
}
