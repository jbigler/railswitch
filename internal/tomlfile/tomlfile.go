// Package tomlfile reads schema-versioned TOML files strictly and writes files
// atomically (D39).
package tomlfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// NewerSchemaError reports a file whose schema is above the one this
// railswitch reads.
type NewerSchemaError struct{ Schema, Max int }

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("schema: %d was written by a newer railswitch (this one reads schema = %d)", e.Schema, e.Max)
}

// Decode reads the top-level schema on its own first, so a file above
// maxSchema is refused as *NewerSchemaError even when it changes a known
// key's type; then it decodes data into v strictly, naming every key v does
// not define. A missing schema decodes as 0; callers decide how to report it.
func Decode(data []byte, maxSchema int, v any) error {
	var head struct {
		Schema int `toml:"schema"`
	}
	if _, err := toml.Decode(string(data), &head); err != nil {
		return err
	}
	if head.Schema > maxSchema {
		return &NewerSchemaError{Schema: head.Schema, Max: maxSchema}
	}
	md, err := toml.Decode(string(data), v)
	if err != nil {
		return err
	}
	und := md.Undecoded()
	errs := make([]error, len(und))
	for i, k := range und {
		errs[i] = fmt.Errorf("unknown key %s", k)
	}
	return errors.Join(errs...)
}

// WriteAtomic replaces path with data: a temp file in the same directory is
// written, fsynced and renamed over path, then the directory is fsynced. A
// crash leaves the old file or the new one, never a partial one.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	err = tmp.Chmod(perm)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
