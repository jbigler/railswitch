package web

import (
	"errors"
	"strings"
)

// SplitArgs splits s into words as a shell would, without any expansion:
// blanks separate words; '…' keeps everything literally; "…" keeps
// everything but a backslash before " \ $ or `, which escapes it; outside
// quotes a backslash escapes the next character. An unterminated quote or
// a trailing backslash is an error.
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case ' ', '\t', '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
			i++
		case '\\':
			if i+1 >= len(s) {
				return nil, errors.New("trailing backslash")
			}
			cur.WriteByte(s[i+1])
			inWord = true
			i += 2
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated ' quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			inWord = true
			i += j + 2
		case '"':
			i++
			for {
				if i >= len(s) {
					return nil, errors.New(`unterminated " quote`)
				}
				c := s[i]
				if c == '"' {
					i++
					break
				}
				if c == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					cur.WriteByte(s[i+1])
					i += 2
					continue
				}
				cur.WriteByte(c)
				i++
			}
			inWord = true
		default:
			cur.WriteByte(c)
			inWord = true
			i++
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}
