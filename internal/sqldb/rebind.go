package sqldb

import (
	"fmt"
	"strconv"
	"strings"
)

// Coordination id: sqldb_rebind_v2
//
// RebindPostgres rewrites "?" placeholders to $1..$n with a PostgreSQL-aware
// lexer and returns the placeholder count. Lexical contract (PostgreSQL with
// standard_conforming_strings=on, the server default since 9.1):
//   - '...'   string; ” is a quote; no backslash escapes.
//   - E'...'  escape string (E/e not preceded by an identifier char);
//     backslash escapes the next char; ” is a quote.
//   - "..."   quoted identifier; "" is a quote.
//   - $tag$...$tag$ / $$...$$ dollar quote ($ after an identifier char is an
//     identifier char; $<digit> is a numbered parameter).
//   - -- line comment up to LF (CRLF ends at its LF); /* */ nested comments.
//   - "?" inside any of the above is left as text.
//
// Refused (error, never a guessed binding): unterminated string, identifier,
// dollar quote or comment; a MySQL backtick; jsonb ?-operators "?|" "?&"
// and "??" outside literals; mixing "?" with numbered $n parameters.
// The jsonb "?" (key exists) operator cannot be told apart from a
// placeholder lexically: this codebase's contract is that PG text never uses
// it (inventory audit); write jsonb_exists(...) instead.
func RebindPostgres(q string) (string, int, error) {
	var b strings.Builder
	b.Grow(len(q) + 8)
	n, numbered := 0, false
	isIdent := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == '\'':
			esc := i > 0 && (q[i-1] == 'E' || q[i-1] == 'e') && (i < 2 || !isIdent(q[i-2]))
			j := i + 1
			for {
				if j >= len(q) {
					return "", 0, fmt.Errorf("unterminated string literal at byte %d", i)
				}
				if esc && q[j] == '\\' {
					j += 2
					continue
				}
				if q[j] == '\'' {
					if j+1 < len(q) && q[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			b.WriteString(q[i : j+1])
			i = j + 1
		case c == '"':
			j := i + 1
			for {
				if j >= len(q) {
					return "", 0, fmt.Errorf("unterminated quoted identifier at byte %d", i)
				}
				if q[j] == '"' {
					if j+1 < len(q) && q[j+1] == '"' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			b.WriteString(q[i : j+1])
			i = j + 1
		case c == '$':
			if i+1 < len(q) && q[i+1] >= '0' && q[i+1] <= '9' {
				numbered = true
				b.WriteByte(c)
				i++
				continue
			}
			if i > 0 && isIdent(q[i-1]) {
				b.WriteByte(c)
				i++
				continue
			}
			k := i + 1
			for k < len(q) && isIdent(q[k]) && !(k == i+1 && q[k] >= '0' && q[k] <= '9') {
				k++
			}
			if k < len(q) && q[k] == '$' {
				tag := q[i : k+1]
				end := strings.Index(q[k+1:], tag)
				if end < 0 {
					return "", 0, fmt.Errorf("unterminated dollar quote %s at byte %d", tag, i)
				}
				stop := k + 1 + end + len(tag)
				b.WriteString(q[i:stop])
				i = stop
				continue
			}
			b.WriteByte(c)
			i++
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			k := strings.IndexByte(q[i:], '\n')
			stop := len(q)
			if k >= 0 {
				stop = i + k
			}
			b.WriteString(q[i:stop])
			i = stop
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			depth, j := 1, i+2
			for depth > 0 {
				if j >= len(q) {
					return "", 0, fmt.Errorf("unterminated block comment at byte %d", i)
				}
				if j+1 < len(q) && q[j] == '/' && q[j+1] == '*' {
					depth++
					j += 2
					continue
				}
				if j+1 < len(q) && q[j] == '*' && q[j+1] == '/' {
					depth--
					j += 2
					continue
				}
				j++
			}
			b.WriteString(q[i:j])
			i = j
		case c == '`':
			return "", 0, fmt.Errorf("MySQL backtick identifier at byte %d in PostgreSQL text", i)
		case c == '?':
			if i+1 < len(q) && (q[i+1] == '|' || q[i+1] == '&' || q[i+1] == '?') {
				return "", 0, fmt.Errorf("ambiguous jsonb ?-operator %q at byte %d", q[i:i+2], i)
			}
			n++
			b.WriteString("$" + strconv.Itoa(n))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	if numbered && n > 0 {
		return "", 0, fmt.Errorf("mixed ? and numbered $n placeholders")
	}
	return b.String(), n, nil
}
