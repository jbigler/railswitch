package libraries

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// Schema is the schema of libraries.toml, fetch.toml and pending.toml.
const Schema = 1

// Record is <data>/libraries/<alias>/fetch.toml: ID, Commit, Serial and
// Signer are current/'s (empty when there is none), Status, Fetched and
// Reason the last fetch's verdict.
type Record struct {
	Schema  int            `toml:"schema"`
	ID      string         `toml:"id"`
	Commit  string         `toml:"commit"`
	Serial  int64          `toml:"serial"`
	Status  signing.Status `toml:"status"`
	Fetched time.Time      `toml:"fetched"`
	Signer  string         `toml:"signer"`
	Reason  string         `toml:"reason"`
}

// Dir is the alias's directory in the data dir.
func Dir(d machine.Dirs, alias string) string {
	return filepath.Join(d.Data, "libraries", alias)
}

// CurrentDir is the alias's verified checkout.
func CurrentDir(d machine.Dirs, alias string) string {
	return filepath.Join(Dir(d, alias), "current")
}

func recordPath(d machine.Dirs, alias string) string {
	return filepath.Join(Dir(d, alias), "fetch.toml")
}

// ReadRecord is the alias's fetch.toml; a library never fetched is an
// fs.ErrNotExist error.
func ReadRecord(d machine.Dirs, alias string) (*Record, error) {
	path := recordPath(d, alias)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Record
	if err := tomlfile.Decode(data, Schema, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

// WriteRecord replaces the alias's fetch.toml.
func WriteRecord(d machine.Dirs, alias string, r *Record) error {
	r.Schema = Schema
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(r); err != nil {
		return err
	}
	return tomlfile.WriteAtomic(recordPath(d, alias), b.Bytes(), 0o644)
}
