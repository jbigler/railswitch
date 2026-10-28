package signing

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// TrustFile is signers.toml's name in the config directory.
const TrustFile = "signers.toml"

// Trust is signers.toml: the keys trusted per library (D100).
type Trust struct {
	Schema    int        `toml:"schema"`
	Libraries []*Library `toml:"library,omitempty"`
}

// Library is one library's trust: its keys, its revoked fingerprints, the
// highest serial verified so far and the sha256 of that serial's
// library.toml (a serial names one index).
type Library struct {
	ID         string       `toml:"id"`
	LastSerial int64        `toml:"last_serial"`
	LastIndex  string       `toml:"last_index,omitempty"`
	Keys       []Key        `toml:"key,omitempty"`
	Revoked    []Revocation `toml:"revoked,omitempty"`
}

// Key is one trusted key.
type Key struct {
	Key         string `toml:"key"`
	Fingerprint string `toml:"fingerprint"`
	Added       Date   `toml:"added"`
}

// Date is a calendar day, written as a TOML local date.
type Date struct{ time.Time }

// MarshalTOML writes the day as a bare TOML local date.
func (d Date) MarshalTOML() ([]byte, error) { return []byte(d.Format(time.DateOnly)), nil }

// UnmarshalTOML reads a TOML date.
func (d *Date) UnmarshalTOML(v any) error {
	t, ok := v.(time.Time)
	if !ok {
		return fmt.Errorf("%v is not a date", v)
	}
	d.Time = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return nil
}

// today is the local calendar day.
func today() Date {
	y, m, d := time.Now().Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// LoadTrust reads signers.toml; a missing file is an empty trust store.
func LoadTrust(path string) (*Trust, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Trust{Schema: Schema}, nil
	}
	if err != nil {
		return nil, err
	}
	var t Trust
	if err := tomlfile.Decode(data, Schema, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := t.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

func (t *Trust) check() error {
	if t.Schema != Schema {
		return fmt.Errorf("schema = %d, want %d", t.Schema, Schema)
	}
	var errs []error
	seen := map[string]bool{}
	for _, l := range t.Libraries {
		if err := CheckLibrary(l.ID); err != nil {
			errs = append(errs, err)
		}
		if seen[l.ID] {
			errs = append(errs, fmt.Errorf("library %s is listed twice", l.ID))
		}
		seen[l.ID] = true
		for _, k := range l.Keys {
			fp, err := Fingerprint(k.Key)
			canon, _ := ParseKey(k.Key)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("library %s: %w", l.ID, err))
			case canon != k.Key:
				errs = append(errs, fmt.Errorf("library %s: key %q is not \"<type> <base64>\" as signer add writes it", l.ID, k.Key))
			case fp != k.Fingerprint:
				errs = append(errs, fmt.Errorf("library %s: key %s has fingerprint %s, the file says %s", l.ID, k.Key, fp, k.Fingerprint))
			}
		}
		if l.LastIndex != "" && !sha256Re.MatchString(l.LastIndex) {
			errs = append(errs, fmt.Errorf("library %s: last_index %q is not 64 lowercase hex digits", l.ID, l.LastIndex))
		}
		for _, r := range l.Revoked {
			if !fingerprintRe.MatchString(r.Fingerprint) {
				errs = append(errs, fmt.Errorf("library %s: revoked %q is not a SHA256: fingerprint", l.ID, r.Fingerprint))
			}
		}
	}
	return errors.Join(errs...)
}

// UpdateTrust loads signers.toml at path, lets fn change it and saves it
// when fn returns save, all under an exclusive flock on path+".lock"
// (0600): two writers that each loaded, changed and saved would otherwise
// drop one's change, a revocation or a higher last_serial. The only way
// to write signers.toml. fn's error is returned and nothing is saved.
func UpdateTrust(path string, fn func(*Trust) (save bool, err error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close() // releases the flock
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	t, err := LoadTrust(path)
	if err != nil {
		return err
	}
	save, err := fn(t)
	if err != nil || !save {
		return err
	}
	return t.save(path)
}

// save writes t to path atomically, 0600.
func (t *Trust) save(path string) error {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(t); err != nil {
		return err
	}
	return tomlfile.WriteAtomic(path, b.Bytes(), 0o600)
}

// Library is lib's entry, or nil.
func (t *Trust) Library(lib string) *Library {
	for _, l := range t.Libraries {
		if l.ID == lib {
			return l
		}
	}
	return nil
}

// Add trusts key for lib and returns its fingerprint. A key already
// trusted is left as it is; a revoked one is refused.
func (t *Trust) Add(lib, key string) (string, error) {
	if err := CheckLibrary(lib); err != nil {
		return "", err
	}
	k, err := ParseKey(key)
	if err != nil {
		return "", err
	}
	fp, _ := Fingerprint(k)
	l := t.Library(lib)
	if l == nil {
		l = &Library{ID: lib}
		t.Libraries = append(t.Libraries, l)
	}
	switch {
	case l.revoked(fp):
		return "", fmt.Errorf("%s is revoked for %s", fp, lib)
	case l.key(fp) != nil:
		return fp, nil
	}
	l.Keys = append(l.Keys, Key{Key: k, Fingerprint: fp, Added: today()})
	return fp, nil
}

// Remove distrusts fp for lib. An entry left with no keys is removed
// unless it holds revocations or a verified serial: dropping those would
// let a revoked key or an older index back in.
func (t *Trust) Remove(lib, fp string) error {
	l := t.Library(lib)
	if l == nil || l.key(fp) == nil {
		return fmt.Errorf("%s is not trusted for %s", fp, lib)
	}
	l.Keys = slices.DeleteFunc(l.Keys, func(k Key) bool { return k.Fingerprint == fp })
	if len(l.Keys) == 0 && len(l.Revoked) == 0 && l.LastSerial == 0 {
		t.Libraries = slices.DeleteFunc(t.Libraries, func(x *Library) bool { return x == l })
	}
	return nil
}

// Revoke moves fp to lib's revoked list, from its keys if it is one: it is
// never trusted again for lib, by Add, library trust or a rotation. A
// fingerprint not trusted yet can be revoked ahead.
func (t *Trust) Revoke(lib, fp string) error {
	if err := CheckLibrary(lib); err != nil {
		return err
	}
	if !fingerprintRe.MatchString(fp) {
		return fmt.Errorf("%q is not a SHA256: fingerprint", fp)
	}
	l := t.Library(lib)
	if l == nil {
		l = &Library{ID: lib}
		t.Libraries = append(t.Libraries, l)
	}
	l.revoke(fp)
	return nil
}

// key is the trusted key with fingerprint fp, or nil. A revoked one is
// never trusted, even if a hand edit left it among the keys.
func (l *Library) key(fp string) *Key {
	if l == nil || l.revoked(fp) {
		return nil
	}
	for i := range l.Keys {
		if l.Keys[i].Fingerprint == fp {
			return &l.Keys[i]
		}
	}
	return nil
}

func (l *Library) revoked(fp string) bool {
	return l != nil && slices.ContainsFunc(l.Revoked, func(r Revocation) bool { return r.Fingerprint == fp })
}

// trustedKeys are l's keys that are not revoked.
func (l *Library) trustedKeys() []string {
	var out []string
	for _, k := range l.Keys {
		if !l.revoked(k.Fingerprint) {
			out = append(out, k.Key)
		}
	}
	return out
}

// revoke moves fp from l's keys to its revoked list.
func (l *Library) revoke(fp string) {
	l.Keys = slices.DeleteFunc(l.Keys, func(k Key) bool { return k.Fingerprint == fp })
	if !l.revoked(fp) {
		l.Revoked = append(l.Revoked, Revocation{Fingerprint: fp})
	}
}
