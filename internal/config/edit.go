package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// Line-level edits of project.toml: railswitch inserts or extends one
// statement and keeps every other byte (comments, order, spacing) of the
// user's file. Each edit re-parses its result with Load + Validate before it
// replaces the file (temp + rename).

// SetImageBase sets [image] base to value, adding [image] and/or base as
// needed. The same value is a no-op; a different existing value is an error
// naming it.
func SetImageBase(path, value string) error {
	return editImage(path, func(p *Project, d *doc) (string, error) {
		switch p.Image.Base {
		case value:
			return d.data, nil
		case "":
			return d.insertKey([]string{"image"}, "base", QuoteString(value)), nil
		}
		return "", fmt.Errorf("%s: [image] base is already %q, not %q: change it by hand", path, p.Image.Base, value)
	})
}

// AppendImageAddon appends value to [image] addons, adding [image] and/or
// addons as needed and keeping the order of the existing entries. An entry
// already listed is an error.
func AppendImageAddon(path, value string) error {
	return editImage(path, func(p *Project, d *doc) (string, error) {
		if slices.Contains(p.Image.Addons, value) {
			return "", fmt.Errorf("%s: [image] addons already lists %q", path, value)
		}
		elem := QuoteString(value)
		s := d.find([]string{"image"}, "addons")
		if s == nil {
			return d.insertKey([]string{"image"}, "addons", "["+elem+"]"), nil
		}
		return d.appendToArray(s, elem)
	})
}

// editImage applies edit to path's [image] table, which must be a plain
// [image] table holding only plain keys (or absent).
func editImage(path string, edit func(*Project, *doc) (string, error)) error {
	return editFile(path, func(d *doc) error {
		for _, s := range d.stmts {
			if len(s.path) == 0 || s.path[0] != "image" {
				continue
			}
			plain := len(s.path) == 1 && !s.array // the [image] header
			if !s.header {
				plain = len(s.table) == 1 && len(s.key) == 1 // a key in [image]
			}
			if !plain {
				return fmt.Errorf("%s:%d: [image] is not a plain [image] table with plain keys (an inline table or a dotted key): edit [image] in project.toml by hand", path, d.line(s.start))
			}
		}
		return nil
	}, edit)
}

// editFile reads path and splits it into statements; check refuses a
// shape the edit can't make by line (checked before decoding, so it is
// reported even when the file does not decode). It then decodes the file
// (Load) and edit returns the new contents. An unchanged result writes
// nothing; any other result must Load and Validate before it replaces path.
func editFile(path string, check func(*doc) error, edit func(*Project, *doc) (string, error)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d, err := parseDoc(string(data))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := check(d); err != nil {
		return err
	}
	p, err := decode(path, data)
	if err != nil {
		return err
	}
	out, err := edit(p, d)
	if err != nil {
		return err
	}
	if out == d.data {
		return nil
	}
	np, err := decode(path, []byte(out))
	if err != nil {
		return fmt.Errorf("edited %w", err)
	}
	if err := np.Validate(); err != nil {
		return fmt.Errorf("%s after the edit: %w", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return tomlfile.WriteAtomic(path, []byte(out), fi.Mode().Perm())
}

// doc is a TOML file split into its statements (headers and key/values);
// blank and comment-only lines belong to no statement.
type doc struct {
	data  string
	stmts []stmt
}

type stmt struct {
	start, end int      // byte range: the statement's lines, end after its last newline (or at EOF)
	header     bool     // a [table] or [[array]] header
	array      bool     // [[array]] header
	table      []string // the table the statement is in; for a header, the table it opens
	key        []string // key/value: the key as written, relative to table
	path       []string // header: table; key/value: table + key
	value      [2]int   // key/value: the value's byte range, without comments
}

// parseDoc splits data into statements. It reads only the structure edits
// need (headers, keys, where a value ends); Load checks everything else.
func parseDoc(data string) (*doc, error) {
	d := &doc{data: data}
	var table []string
	for i := 0; i < len(data); {
		j := skipBlank(data, i)
		if j == len(data) {
			break
		}
		switch data[j] {
		case '\n', '\r', '#':
			i = lineEnd(data, j)
			continue
		case '[':
			s := stmt{start: i, header: true, array: strings.HasPrefix(data[j:], "[[")}
			k := j + 1
			if s.array {
				k++
			}
			path, k, err := parseKey(data, k)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", d.line(j), err)
			}
			closer := "]"
			if s.array {
				closer = "]]"
			}
			if !strings.HasPrefix(data[k:], closer) {
				return nil, fmt.Errorf("line %d: table header lacks %s", d.line(j), closer)
			}
			table = path
			s.table, s.path, s.end = path, path, lineEnd(data, k)
			d.stmts = append(d.stmts, s)
			i = s.end
		default:
			key, k, err := parseKey(data, j)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", d.line(j), err)
			}
			if k == len(data) || data[k] != '=' {
				return nil, fmt.Errorf("line %d: key lacks =", d.line(j))
			}
			start := skipBlank(data, k+1)
			end, err := scanValue(data, start)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", d.line(j), err)
			}
			s := stmt{start: i, table: table, key: key, path: slices.Concat(table, key), value: [2]int{start, end}, end: lineEnd(data, end)}
			d.stmts = append(d.stmts, s)
			i = s.end
		}
	}
	return d, nil
}

// line is the 1-based line number of byte offset off.
func (d *doc) line(off int) int { return strings.Count(d.data[:off], "\n") + 1 }

// find returns the key/value statement key in table, or nil.
func (d *doc) find(table []string, key string) *stmt {
	for i, s := range d.stmts {
		if !s.header && slices.Equal(s.table, table) && slices.Equal(s.key, []string{key}) {
			return &d.stmts[i]
		}
	}
	return nil
}

// insertKey returns data with `key = value` added to table: after the
// table's last key/value, or as a new table at the end of the file. key
// must be a bare key; value is TOML.
func (d *doc) insertKey(table []string, key, value string) string {
	line := key + " = " + value + "\n"
	at := -1
	for _, s := range d.stmts {
		switch {
		case s.header && slices.Equal(s.path, table) && !s.array:
			at = s.end
		case s.header:
			if at >= 0 {
				return insertAt(d.data, at, line)
			}
		case at >= 0:
			at = s.end
		}
	}
	if at >= 0 {
		return insertAt(d.data, at, line)
	}
	return d.appendTable(table, line)
}

// appendTable returns data with a new [table] holding body at the end,
// after a blank line.
func (d *doc) appendTable(table []string, body string) string {
	var b strings.Builder
	b.WriteString(d.data)
	if d.data != "" {
		if !strings.HasSuffix(d.data, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString("[" + strings.Join(table, ".") + "]\n")
	b.WriteString(body)
	return b.String()
}

// insertAt inserts the line text at offset at, which is a line start or
// the end of a file lacking its last newline.
func insertAt(data string, at int, text string) string {
	if at > 0 && data[at-1] != '\n' {
		text = "\n" + text
	}
	return data[:at] + text + data[at:]
}

// appendToArray returns data with elem (TOML) appended to the array value
// of s, right after its last element; comments and line breaks stay.
func (d *doc) appendToArray(s *stmt, elem string) (string, error) {
	data := d.data
	open, end := s.value[0], s.value[1]
	if data[open] != '[' || data[end-1] != ']' {
		return "", fmt.Errorf("line %d: %s is not an array: edit it by hand", d.line(s.start), strings.Join(s.key, "."))
	}
	last, lastByte, depth := open+1, byte('['), 1
	for p := open + 1; p < end; {
		e, err := tokenEnd(data, p)
		if err != nil {
			return "", err
		}
		switch c := data[p]; c {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
		if depth == 0 {
			if p != end-1 {
				return "", fmt.Errorf("line %d: %s is not an array: edit it by hand", d.line(s.start), strings.Join(s.key, "."))
			}
			break
		}
		if c := data[p]; c != ' ' && c != '\t' && c != '\r' && c != '\n' && c != '#' {
			last, lastByte = e, data[e-1]
		}
		p = e
	}
	switch lastByte {
	case '[':
	case ',':
		elem = " " + elem
	default:
		elem = ", " + elem
	}
	return data[:last] + elem + data[last:], nil
}

// skipBlank skips spaces and tabs.
func skipBlank(data string, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	return i
}

// lineEnd is the offset after the newline ending the line at i, or EOF.
func lineEnd(data string, i int) int {
	if n := strings.IndexByte(data[i:], '\n'); n >= 0 {
		return i + n + 1
	}
	return len(data)
}

// parseKey reads a dotted key (bare, "basic" or 'literal' parts) at i and
// returns its parts and the offset after it and its trailing blanks.
func parseKey(data string, i int) ([]string, int, error) {
	var parts []string
	for {
		i = skipBlank(data, i)
		if i == len(data) {
			return nil, i, errors.New("missing key")
		}
		switch data[i] {
		case '"', '\'':
			e, err := tokenEnd(data, i)
			if err != nil {
				return nil, i, err
			}
			raw := data[i:e]
			part := raw[1 : len(raw)-1]
			if raw[0] == '"' {
				if u, err := strconv.Unquote(raw); err == nil {
					part = u
				}
			}
			parts, i = append(parts, part), e
		default:
			e := i
			for e < len(data) && isBareKeyByte(data[e]) {
				e++
			}
			if e == i {
				return nil, i, fmt.Errorf("unexpected %q in a key", data[i])
			}
			parts, i = append(parts, data[i:e]), e
		}
		i = skipBlank(data, i)
		if i == len(data) || data[i] != '.' {
			return parts, i, nil
		}
		i++
	}
}

func isBareKeyByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// scanValue returns the end of the value starting at i: after its last
// significant byte, before a trailing comment. A value spans lines only
// inside brackets or a multi-line string.
func scanValue(data string, i int) (int, error) {
	last, depth := -1, 0
	for p := i; p < len(data); {
		c := data[p]
		if c == '\n' && depth == 0 {
			break
		}
		e, err := tokenEnd(data, p)
		if err != nil {
			return 0, err
		}
		switch c {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' && c != '#' {
			last = e
		}
		p = e
	}
	if last < 0 {
		return 0, errors.New("missing value")
	}
	if depth != 0 {
		return 0, errors.New("unclosed bracket")
	}
	return last, nil
}

// tokenEnd returns the end of the token at p: a whole string (basic,
// literal or multi-line), a comment up to its newline, or one byte.
func tokenEnd(data string, p int) (int, error) {
	rest := data[p:]
	switch {
	case strings.HasPrefix(rest, `"""`), strings.HasPrefix(rest, `'''`):
		delim := rest[:3]
		for q := 3; q < len(rest); q++ {
			if delim[0] == '"' && rest[q] == '\\' {
				q++
				continue
			}
			if strings.HasPrefix(rest[q:], delim) {
				q += 3
				// Up to two quotes before the delimiter belong to the string.
				for n := 0; n < 2 && q < len(rest) && rest[q] == delim[0]; n++ {
					q++
				}
				return p + q, nil
			}
		}
		return 0, errors.New("unclosed multi-line string")
	case rest[0] == '"', rest[0] == '\'':
		for q := 1; q < len(rest) && rest[q] != '\n'; q++ {
			if rest[0] == '"' && rest[q] == '\\' {
				q++
				continue
			}
			if rest[q] == rest[0] {
				return p + q + 1, nil
			}
		}
		return 0, errors.New("unclosed string")
	case rest[0] == '#':
		if n := strings.IndexByte(rest, '\n'); n >= 0 {
			return p + n, nil
		}
		return len(data), nil
	}
	return p + 1, nil
}

// QuoteString quotes s as a TOML basic string.
func QuoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// AddSecret appends a [secrets.<name>] table holding s's non-empty fields at
// the end of path. A secret name already declared is an error.
func AddSecret(path, name string, s Secret) error {
	return editFile(path, func(*doc) error { return nil }, func(p *Project, d *doc) (string, error) {
		if _, ok := p.Secrets[name]; ok {
			return "", fmt.Errorf("%s: secret %s is already declared", path, name)
		}
		if !secretNameRe.MatchString(name) {
			return "", fmt.Errorf("secret name %q must match %s", name, secretNameRe)
		}
		var b strings.Builder
		if len(s.Services) > 0 {
			elems := make([]string, len(s.Services))
			for i, id := range s.Services {
				elems[i] = QuoteString(id)
			}
			b.WriteString("services = [" + strings.Join(elems, ", ") + "]\n")
		}
		for _, kv := range [][2]string{{"env", s.Env}, {"scope", s.Scope}, {"git_host", s.GitHost}, {"git_user", s.GitUser}} {
			if kv[1] != "" {
				b.WriteString(kv[0] + " = " + QuoteString(kv[1]) + "\n")
			}
		}
		if s.GitSigning {
			b.WriteString("git_signing = true\n")
		}
		return d.appendTable([]string{"secrets", name}, b.String()), nil
	})
}
