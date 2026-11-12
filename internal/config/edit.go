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

// SetImageBaseText sets [image] base to value, adding [image] and/or base as
// needed. The same value is a no-op; a different existing value is an error
// naming it.
func SetImageBaseText(path, data, value string) (string, error) {
	return editImage(path, data, func(p *Project, d *doc) (string, error) {
		switch p.Image.Base {
		case value:
			return d.data, nil
		case "":
			return d.insertKey([]string{"image"}, "base", QuoteString(value)), nil
		}
		return "", fmt.Errorf("%s: [image] base is already %q, not %q: change it by hand", path, p.Image.Base, value)
	})
}

// AppendImageAddonText appends value to [image] addons, adding [image] and/or
// addons as needed and keeping the order of the existing entries. An entry
// already listed is an error.
func AppendImageAddonText(path, data, value string) (string, error) {
	return editImage(path, data, func(p *Project, d *doc) (string, error) {
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

// AppendImageAddon is AppendImageAddonText on the file at path.
func AppendImageAddon(path, value string) error {
	return editFile(path, func(data string) (string, error) { return AppendImageAddonText(path, data, value) })
}

// editImage applies edit to path's [image] table, which must be a plain
// [image] table holding only plain keys (or absent).
func editImage(path, data string, edit func(*Project, *doc) (string, error)) (string, error) {
	return editText(path, data, func(d *doc) error {
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

// editFile is editText on the file at path: an unchanged result writes
// nothing, any other replaces path (temp + rename).
func editFile(path string, edit func(data string) (string, error)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := edit(string(data))
	if err != nil || out == string(data) {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return tomlfile.WriteAtomic(path, []byte(out), fi.Mode().Perm())
}

// editText splits data, the text of the project.toml at path (named in
// errors), into statements; check refuses a shape the edit can't make by
// line (checked before decoding, so it is reported even when the text does
// not decode). It then decodes the text and edit returns the new contents,
// which must decode and Validate unless they are unchanged.
func editText(path, data string, check func(*doc) error, edit func(*Project, *doc) (string, error)) (string, error) {
	d, err := parseDoc(data)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if err := check(d); err != nil {
		return "", err
	}
	p, err := decode(path, []byte(data))
	if err != nil {
		return "", err
	}
	out, err := edit(p, d)
	if err != nil {
		return "", err
	}
	if out == d.data {
		return out, nil
	}
	np, err := decode(path, []byte(out))
	if err != nil {
		return "", fmt.Errorf("edited %w", err)
	}
	if err := np.Validate(); err != nil {
		return "", fmt.Errorf("%s after the edit: %w", path, err)
	}
	return out, nil
}

func noCheck(*doc) error { return nil }

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
	return d.appendBlock("["+strings.Join(table, ".")+"]", body)
}

// appendBlock is appendTable for any header line.
func (d *doc) appendBlock(header, body string) string {
	var b strings.Builder
	b.WriteString(d.data)
	if d.data != "" {
		if !strings.HasSuffix(d.data, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(header + "\n")
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

// AddSecretText appends a [secrets.<name>] table holding s's non-empty fields at
// the end of path. A secret name already declared is an error.
func AddSecretText(path, data, name string, s Secret) (string, error) {
	return editText(path, data, noCheck, func(p *Project, d *doc) (string, error) {
		if _, ok := p.Secrets[name]; ok {
			return "", fmt.Errorf("%s: secret %s is already declared", path, name)
		}
		if !secretNameRe.MatchString(name) {
			return "", fmt.Errorf("secret name %q must match %s", name, secretNameRe)
		}
		return d.appendTable([]string{"secrets", name}, SecretTOML(s)), nil
	})
}

// AddSecret is AddSecretText on the file at path.
func AddSecret(path, name string, s Secret) error {
	return editFile(path, func(data string) (string, error) { return AddSecretText(path, data, name, s) })
}

// AddAgentText appends an [agents.<name>] table holding a's use and, when set,
// run at the end of path. An agent name already declared is an error.
func AddAgentText(path, data, name string, a Agent) (string, error) {
	return editText(path, data, noCheck, func(p *Project, d *doc) (string, error) {
		if _, ok := p.Agents[name]; ok {
			return "", fmt.Errorf("%s: agent %s is already declared", path, name)
		}
		body := "use = " + QuoteString(a.Use) + "\n"
		if a.Run != "" {
			body += "run = " + QuoteString(a.Run) + "\n"
		}
		return d.appendTable([]string{"agents", name}, body), nil
	})
}

// AddSecretServiceText adds service to secret name's services in path, as
// the array's last element (or a new services key); a service already
// there changes nothing.
func AddSecretServiceText(path, data, name, service string) (string, error) {
	return editText(path, data, noCheck, func(p *Project, d *doc) (string, error) {
		s, ok := p.Secrets[name]
		if !ok {
			return "", fmt.Errorf("%s: secret %s is not declared", path, name)
		}
		if slices.Contains(s.Services, service) {
			return d.data, nil
		}
		table := []string{"secrets", name}
		if st := d.find(table, "services"); st != nil {
			return d.appendToArray(st, QuoteString(service))
		}
		return d.insertKey(table, "services", "["+QuoteString(service)+"]"), nil
	})
}

// The edits below work on file text and do not validate: the caller loads
// and validates the result (settings judges it before apply writes it).

// BlockedError is a shape the line editor can't edit: an inline table or a
// dotted key where it needs a plain table.
type BlockedError struct {
	Line int
	Msg  string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("project.toml:%d: %s: edit project.toml directly", e.Line, e.Msg)
}

// SetOption sets [options] key to ref. A slot keeps ref "none" as a value; any
// other key set to "none" loses its line (an absent one stays absent).
func SetOption(data, key, ref string, slot bool) (string, error) {
	d, err := parseEdit(data, "options", false, false)
	if err != nil {
		return "", err
	}
	table := []string{"options"}
	s := d.find(table, key)
	if !slot && ref == "none" {
		if s == nil {
			return data, nil
		}
		return d.removeStmt(s), nil
	}
	if s != nil {
		return d.setValue(s, QuoteString(ref)), nil
	}
	return d.insertKey(table, keyTOML(key), QuoteString(ref)), nil
}

// SetImageAddons sets [image] addons to addons (removing the key when
// addons is empty); a project.toml without [image] gets one.
func SetImageAddons(data string, addons []string) (string, error) {
	d, err := parseEdit(data, "image", false, false)
	if err != nil {
		return "", err
	}
	value := stringArray(addons)
	s := d.find([]string{"image"}, "addons")
	switch {
	case s != nil && len(addons) == 0:
		return d.removeStmt(s), nil
	case s != nil:
		return d.setValue(s, value), nil
	case len(addons) == 0:
		return data, nil
	}
	return d.insertKey([]string{"image"}, "addons", value), nil
}

// SetPack sets [stack] pack to ref; [stack] app stays (Settings offers
// only packs with the same app service).
func SetPack(data, ref string) (string, error) {
	d, err := parseEdit(data, "stack", false, false)
	if err != nil {
		return "", err
	}
	s := d.find([]string{"stack"}, "pack")
	if s == nil {
		return "", errors.New("project.toml has no [stack] pack")
	}
	return d.setValue(s, QuoteString(ref)), nil
}

// SetEnv sets the project [env] name to value.
func SetEnv(data, name, value string) (string, error) {
	d, err := parseEdit(data, "env", false, false)
	if err != nil {
		return "", err
	}
	table := []string{"env"}
	if s := d.find(table, name); s != nil {
		return d.setValue(s, QuoteString(value)), nil
	}
	return d.insertKey(table, keyTOML(name), QuoteString(value)), nil
}

// SetTemplateSHA sets [template] sha256 to sha (template check --accept,
// D111); a project.toml with no [template] ref is an error.
func SetTemplateSHA(data, sha string) (string, error) {
	d, err := parseEdit(data, "template", false, false)
	if err != nil {
		return "", err
	}
	table := []string{"template"}
	if d.find(table, "ref") == nil {
		return "", errors.New("project.toml: [template] has no ref")
	}
	if s := d.find(table, "sha256"); s != nil {
		return d.setValue(s, QuoteString(sha)), nil
	}
	return d.insertKey(table, "sha256", QuoteString(sha)), nil
}

// UnsetEnv removes the project [env] name; an absent name is an error.
func UnsetEnv(data, name string) (string, error) {
	d, err := parseEdit(data, "env", false, false)
	if err != nil {
		return "", err
	}
	s := d.find([]string{"env"}, name)
	if s == nil {
		return "", fmt.Errorf("project.toml: [env] has no %s", name)
	}
	return d.removeStmt(s), nil
}

// SetTask sets the project [tasks] name to cmd; "" hides the pack's task
// of that name (D156).
func SetTask(data, name, cmd string) (string, error) {
	d, err := parseEdit(data, "tasks", false, false)
	if err != nil {
		return "", err
	}
	table := []string{"tasks"}
	if s := d.find(table, name); s != nil {
		return d.setValue(s, QuoteString(cmd)), nil
	}
	return d.insertKey(table, keyTOML(name), QuoteString(cmd)), nil
}

// UnsetTask removes the project [tasks] name; an absent name is an error.
func UnsetTask(data, name string) (string, error) {
	d, err := parseEdit(data, "tasks", false, false)
	if err != nil {
		return "", err
	}
	s := d.find([]string{"tasks"}, name)
	if s == nil {
		return "", fmt.Errorf("project.toml: [tasks] has no %s", keyTOML(name))
	}
	return d.removeStmt(s), nil
}

// AddLink appends a [[links]] table. A label already used by a project link
// is an error.
func AddLink(data, label, url string) (string, error) {
	d, err := parseEdit(data, "links", true, false)
	if err != nil {
		return "", err
	}
	for _, b := range d.linkBlocks() {
		if b.label == label {
			return "", fmt.Errorf("project.toml: link %q is already declared", label)
		}
	}
	body := "label = " + QuoteString(label) + "\nurl = " + QuoteString(url) + "\n"
	return d.appendBlock("[[links]]", body), nil
}

// RemoveLink removes the [[links]] table labelled label, from its header
// through its last key; comments around it stay.
func RemoveLink(data, label string) (string, error) {
	d, err := parseEdit(data, "links", true, false)
	if err != nil {
		return "", err
	}
	for _, b := range d.linkBlocks() {
		if b.label == label {
			start, rest := b.start, d.data[b.end:]
			// The blank line AddLink put before the table goes with it,
			// unless it is all that separates its neighbours.
			if strings.HasSuffix(d.data[:start], "\n\n") && (rest == "" || strings.HasPrefix(rest, "\n")) {
				start--
			}
			return d.data[:start] + rest, nil
		}
	}
	return "", fmt.Errorf("project.toml: no link %q", label)
}

// HideLink appends label to the top-level hide_links; a hidden label is an
// error.
func HideLink(data, label string) (string, error) {
	return addTopElem(data, "hide_links", label, "link %q is already hidden")
}

// ShowLink removes label from hide_links; a label not hidden is an error.
func ShowLink(data, label string) (string, error) {
	return removeTopElem(data, "hide_links", label, "link %q is not hidden")
}

// AddFreshTask appends name to the top-level fresh_tasks (D171); a name
// already there is an error.
func AddFreshTask(data, name string) (string, error) {
	return addTopElem(data, "fresh_tasks", name, "task %q already runs in a new container")
}

// RemoveFreshTask removes name from fresh_tasks; a name not there is an
// error.
func RemoveFreshTask(data, name string) (string, error) {
	return removeTopElem(data, "fresh_tasks", name, "task %q does not run in a new container")
}

// AddTerminalTask appends name to the top-level terminal_tasks (D175); a
// name already there is an error.
func AddTerminalTask(data, name string) (string, error) {
	return addTopElem(data, "terminal_tasks", name, "task %q already opens in a terminal")
}

// RemoveTerminalTask removes name from terminal_tasks; a name not there is
// an error.
func RemoveTerminalTask(data, name string) (string, error) {
	return removeTopElem(data, "terminal_tasks", name, "task %q does not open in a terminal")
}

// addTopElem appends value to the top-level string array key, creating it;
// a value already there is the error dup.
func addTopElem(data, key, value, dup string) (string, error) {
	d, s, err := parseTopArray(data, key)
	if err != nil {
		return "", err
	}
	elem := QuoteString(value)
	if s == nil {
		return d.insertTop(key + " = [" + elem + "]\n"), nil
	}
	elems, err := d.arrayElems(s)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(elems, func(e arrayElem) bool { return e.value == value }) {
		return "", fmt.Errorf("project.toml: "+dup, value)
	}
	return d.appendToArray(s, elem)
}

// removeTopElem removes value from the top-level array key; an absent key
// is the error missing.
func removeTopElem(data, key, value, missing string) (string, error) {
	d, s, err := parseTopArray(data, key)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", fmt.Errorf("project.toml: "+missing, value)
	}
	return d.removeArrayElem(s, value)
}

func parseTopArray(data, key string) (*doc, *stmt, error) {
	d, err := parseEdit(data, key, false, true)
	if err != nil {
		return nil, nil, err
	}
	s := d.find(nil, key)
	if s != nil && d.data[s.value[0]] != '[' {
		return nil, nil, &BlockedError{d.line(s.start), key + " is not an array"}
	}
	return d, s, nil
}

// parseEdit splits data and checks that everything named root is the plain
// shape the edit works on: a [root] table (a [[root]] one when array) holding
// plain keys, or, for topKey, one top-level root = value key.
func parseEdit(data, root string, array, topKey bool) (*doc, error) {
	d, err := parseDoc(data)
	if err != nil {
		return nil, fmt.Errorf("project.toml: %w", err)
	}
	for _, s := range d.stmts {
		if len(s.path) == 0 || s.path[0] != root {
			continue
		}
		var plain bool
		switch {
		case topKey:
			plain = !s.header && len(s.table) == 0 && len(s.key) == 1
		case s.header:
			plain = len(s.path) == 1 && s.array == array
		default:
			plain = len(s.table) == 1 && len(s.key) == 1
		}
		if !plain {
			return nil, &BlockedError{d.line(s.start), root + " is not a plain table with plain keys (an inline table, a dotted key or another table form)"}
		}
	}
	return d, nil
}

// keyTOML writes k as a bare key, or quoted when it isn't one.
func keyTOML(k string) string {
	if k != "" && !strings.ContainsFunc(k, func(r rune) bool { return r > 0x7f || !isBareKeyByte(byte(r)) }) {
		return k
	}
	return QuoteString(k)
}

// setValue returns data with s's value replaced; a trailing comment stays.
func (d *doc) setValue(s *stmt, value string) string {
	return d.data[:s.value[0]] + value + d.data[s.value[1]:]
}

// removeStmt returns data without the lines of s.
func (d *doc) removeStmt(s *stmt) string {
	return d.data[:s.start] + d.data[s.end:]
}

type linkBlock struct {
	start, end int
	label      string
}

// linkBlocks lists the [[links]] tables: header through last key.
func (d *doc) linkBlocks() []linkBlock {
	var bs []linkBlock
	for _, s := range d.stmts {
		switch {
		case s.header && s.array && len(s.path) == 1 && s.path[0] == "links":
			bs = append(bs, linkBlock{start: s.start, end: s.end})
		case !s.header && len(bs) > 0 && len(s.table) == 1 && s.table[0] == "links":
			b := &bs[len(bs)-1]
			b.end = s.end
			if slices.Equal(s.key, []string{"label"}) {
				b.label, _ = unquoteTOML(d.data[s.value[0]:s.value[1]])
			}
		}
	}
	return bs
}

// unquoteTOML reads a single-line basic or literal string token.
func unquoteTOML(tok string) (string, bool) {
	if len(tok) < 2 || strings.HasPrefix(tok, `"""`) || strings.HasPrefix(tok, `'''`) {
		return "", false
	}
	switch tok[0] {
	case '"':
		u, err := strconv.Unquote(tok)
		return u, err == nil
	case '\'':
		return tok[1 : len(tok)-1], true
	}
	return "", false
}

type arrayElem struct {
	start, end int
	value      string
}

// arrayElems lists the string elements of the array value of s.
func (d *doc) arrayElems(s *stmt) ([]arrayElem, error) {
	data := d.data
	var elems []arrayElem
	depth := 0
	for p := s.value[0]; p < s.value[1]; {
		e, err := tokenEnd(data, p)
		if err != nil {
			return nil, err
		}
		switch c := data[p]; c {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case '"', '\'':
			if v, ok := unquoteTOML(data[p:e]); ok && depth == 1 {
				elems = append(elems, arrayElem{p, e, v})
			}
		}
		p = e
	}
	return elems, nil
}

// removeArrayElem returns data without the string elem in the array of s,
// with its comma; an element alone on its line takes the line along.
func (d *doc) removeArrayElem(s *stmt, elem string) (string, error) {
	elems, err := d.arrayElems(s)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(elems, func(e arrayElem) bool { return e.value == elem })
	if i < 0 {
		return "", fmt.Errorf("line %d: %s has no %q", d.line(s.start), strings.Join(s.key, "."), elem)
	}
	data := d.data
	from, to := elems[i].start, elems[i].end
	if q := skipBlank(data, to); q < len(data) && data[q] == ',' {
		to = skipBlank(data, q+1)
	} else {
		for q := from; q > s.value[0]; {
			q--
			if data[q] == ',' {
				from = q
				break
			}
			if c := data[q]; c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				break
			}
		}
	}
	ls := strings.LastIndexByte(data[:from], '\n') + 1
	if le := lineEnd(data, to); strings.TrimSpace(data[ls:from]) == "" && strings.TrimSpace(data[to:le]) == "" {
		from, to = ls, le
	}
	return data[:from] + data[to:], nil
}

// insertTop returns data with line (a top-level key) after the last
// top-level key, or before the first table.
func (d *doc) insertTop(line string) string {
	at := -1
	for _, s := range d.stmts {
		if s.header {
			if at < 0 {
				return d.data[:s.start] + line + "\n" + d.data[s.start:]
			}
			break
		}
		at = s.end
	}
	if at < 0 {
		at = len(d.data)
	}
	return insertAt(d.data, at, line)
}
