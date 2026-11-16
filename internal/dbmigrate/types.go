package dbmigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// PGName is a deterministic, schema-unique identifier: prefix__parts, cut
// to 63 bytes with a hash suffix when needed.
func PGName(parts ...string) string {
	n := strings.Join(parts, "__")
	if len(n) <= 63 {
		return n
	}
	h := sha256.Sum256([]byte(n))
	return n[:54] + "_" + hex.EncodeToString(h[:])[:8]
}

func pq(id string) string { return `"` + strings.ReplaceAll(id, `"`, `""`) + `"` }

func lit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// EnumValues parses enum('a','b”c') from COLUMN_TYPE.
func EnumValues(colType string) ([]string, error) {
	s := strings.TrimSpace(colType)
	if !strings.HasPrefix(s, "enum(") || !strings.HasSuffix(s, ")") {
		return nil, fmt.Errorf("not an enum: %s", colType)
	}
	s = s[5 : len(s)-1]
	var out []string
	for len(s) > 0 {
		if s[0] != '\'' {
			return nil, fmt.Errorf("enum parse")
		}
		var b strings.Builder
		i := 1
		for ; i < len(s); i++ {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				break
			}
			b.WriteByte(s[i])
		}
		if i >= len(s) {
			return nil, fmt.Errorf("enum parse")
		}
		out = append(out, b.String())
		s = s[i+1:]
		if strings.HasPrefix(s, ",") {
			s = s[1:]
		} else if s != "" {
			return nil, fmt.Errorf("enum parse")
		}
	}
	return out, nil
}

type intRange struct{ pg, min, max string }

var signedRanges = map[string]intRange{
	"tinyint": {"smallint", "-128", "127"}, "smallint": {"smallint", "", ""}, "mediumint": {"integer", "-8388608", "8388607"},
	"int": {"integer", "", ""}, "bigint": {"bigint", "", ""},
}
var unsignedRanges = map[string]intRange{
	"tinyint": {"smallint", "0", "255"}, "smallint": {"integer", "0", "65535"}, "mediumint": {"integer", "0", "16777215"},
	"int": {"bigint", "0", "4294967295"},
	// Full range preserved; identity/FK-linked columns are handled apart.
	"bigint": {"numeric(20,0)", "0", "18446744073709551615"},
}

// ColumnType maps one column to its PG type plus range/semantic CHECKs.
// notes carry documented semantic changes (collation, range).
// ColumnType maps one column. narrow=true is a bigint unsigned column linked
// to an identity (directly or through FKs): PG identities and FK equality need
// bigint, so its max becomes 2^63-1 - recorded as a BLOCKING pending decision.
// pending entries block cutover readiness; notes are informational.
func ColumnType(t Table, c Column, narrow bool) (pg string, checks []string, notes []string, pending []string, err error) {
	k, err := ColumnKind(c)
	if err != nil {
		return "", nil, nil, nil, err
	}
	unsigned := strings.Contains(c.ColType, "unsigned")
	col := pq(c.Name)
	switch k {
	case KInt:
		r := signedRanges[c.DataType]
		if unsigned {
			r = unsignedRanges[c.DataType]
		}
		pg = r.pg
		if r.min != "" {
			checks = append(checks, col+" >= "+r.min)
		}
		if r.max != "" {
			checks = append(checks, col+" <= "+r.max)
		}
		if unsigned && c.DataType == "bigint" && narrow {
			pg, checks = "bigint", []string{col + " >= 0"}
			pending = append(pending, "range_narrowing:bigint_unsigned->bigint(max 2^63-1, identity/FK-linked)")
		}
	case KDec:
		if c.Precision == nil || c.Scale == nil {
			return "", nil, nil, nil, fmt.Errorf("decimal without precision")
		}
		pg = fmt.Sprintf("numeric(%d,%d)", *c.Precision, *c.Scale)
		if unsigned {
			checks = append(checks, col+" >= 0")
		}
	case KFloat, KDouble:
		pg = map[Kind]string{KFloat: "real", KDouble: "double precision"}[k]
		if unsigned {
			checks = append(checks, col+" >= 0")
		}
	case KChar, KText:
		switch {
		case c.DataType == "char":
			pg = fmt.Sprintf("char(%d)", *c.CharLen)
		case c.DataType == "varchar":
			pg = fmt.Sprintf("varchar(%d)", *c.CharLen)
		default:
			pg = "text"
			if c.CharLen != nil { // MySQL TEXT limits are bytes
				checks = append(checks, fmt.Sprintf("octet_length(%s) <= %d", col, *c.CharLen))
			}
		}
	case KEnum:
		pg = pq(PGName("enum", t.Name, c.Name))
	case KDate:
		pg = "date"
	case KTime, KDatetime, KTS:
		p := int64(0)
		if c.DTPrec != nil {
			p = *c.DTPrec
		}
		pg = map[Kind]string{KTime: "time", KDatetime: "timestamp", KTS: "timestamptz"}[k] + fmt.Sprintf("(%d)", p)
	case KJSON:
		pg = "jsonb"
	case KBinary:
		pg = "bytea"
		if c.CharLen != nil {
			checks = append(checks, fmt.Sprintf("octet_length(%s) <= %d", col, *c.CharLen))
		}
	}
	if c.Collation != nil && (k == KText || k == KChar) {
		coll := *c.Collation
		target, err := pgCollationFor(coll)
		if err != nil {
			return "", nil, nil, nil, err
		}
		if target != "" { // pgnative-A: _ci -> default collation (no COLLATE)
			pg += " COLLATE " + target
		}
		// PAD attribute is decided from export metadata in BuildPlan (padPending),
		// never inferred from the collation name.
	}
	return pg, checks, notes, pending, nil
}

// --- expression translation (CHECK / GENERATED / DEFAULT) ---------------

type etok struct {
	kind string // ident, str, num, op, word
	text string
}

func lex(src string) ([]etok, error) {
	// information_schema renders quotes inside expressions as \' ; when the
	// text has no bare quote at all, undo that rendering first.
	if strings.Contains(src, `\'`) && !strings.Contains(strings.ReplaceAll(src, `\'`, ""), "'") {
		src = strings.ReplaceAll(src, `\'`, "'")
	}
	var out []etok
	for i := 0; i < len(src); {
		ch := src[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n':
			i++
		case ch == '`':
			j := strings.IndexByte(src[i+1:], '`')
			if j < 0 {
				return nil, fmt.Errorf("unterminated identifier")
			}
			out = append(out, etok{"ident", src[i+1 : i+1+j]})
			i += j + 2
		case ch == '_' && strings.HasPrefix(src[i:], "_utf8mb4'"):
			i += len("_utf8mb4")
		case ch == '\'':
			var b strings.Builder
			j := i + 1
			for ; j < len(src); j++ {
				if src[j] == '\\' && j+1 < len(src) {
					j++
					b.WriteByte(src[j])
					continue
				}
				if src[j] == '\'' {
					if j+1 < len(src) && src[j+1] == '\'' {
						b.WriteByte('\'')
						j++
						continue
					}
					break
				}
				b.WriteByte(src[j])
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string")
			}
			out = append(out, etok{"str", b.String()})
			i = j + 1
		case ch >= '0' && ch <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			out = append(out, etok{"num", src[i:j]})
			i = j
		case strings.ContainsRune("()=<>!,+-*/", rune(ch)):
			if i+1 < len(src) && (src[i:i+2] == "<=" || src[i:i+2] == ">=" || src[i:i+2] == "<>" || src[i:i+2] == "!=") {
				out = append(out, etok{"op", src[i : i+2]})
				i += 2
			} else {
				out = append(out, etok{"op", string(ch)})
				i++
			}
		case ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_':
			j := i
			for j < len(src) && (src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9' || src[j] == '_') {
				j++
			}
			out = append(out, etok{"word", strings.ToLower(src[i:j])})
			i = j
		default:
			return nil, fmt.Errorf("unsupported character %q in expression", ch)
		}
	}
	return out, nil
}

var exprWords = map[string]string{"and": "AND", "or": "OR", "not": "NOT", "is": "IS", "null": "NULL", "case": "CASE", "when": "WHEN",
	"then": "THEN", "else": "ELSE", "end": "END", "in": "IN", "between": "BETWEEN", "true": "TRUE", "false": "FALSE", "like": "LIKE"}

// TranslateExpr converts a MySQL CHECK/generated expression to PG using an
// allowlist; anything else is an unsupported-feature error.
func TranslateExpr(src string) (string, error) {
	toks, err := lex(src)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := 0; i < len(toks); i++ {
		tk := toks[i]
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		switch tk.kind {
		case "ident":
			b.WriteString(pq(tk.text))
		case "str":
			b.WriteString(lit(tk.text))
		case "num":
			b.WriteString(tk.text)
		case "op":
			if tk.text == "!=" {
				b.WriteString("<>")
			} else {
				b.WriteString(tk.text)
			}
		case "word":
			if w, ok := exprWords[tk.text]; ok {
				b.WriteString(w)
				continue
			}
			if i+1 >= len(toks) || toks[i+1].text != "(" {
				return "", fmt.Errorf("unsupported word %q", tk.text)
			}
			args, next, err := callArgs(toks, i+1)
			if err != nil {
				return "", err
			}
			out, err := translateCall(tk.text, args)
			if err != nil {
				return "", err
			}
			b.WriteString(out)
			i = next
		}
	}
	return b.String(), nil
}

// callArgs returns the raw token groups of f( ... ) and the index of ')'.
func callArgs(toks []etok, open int) ([][]etok, int, error) {
	depth, start := 0, open+1
	var args [][]etok
	for j := open; j < len(toks); j++ {
		switch toks[j].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				if j > start {
					args = append(args, toks[start:j])
				}
				return args, j, nil
			}
		case ",":
			if depth == 1 {
				args = append(args, toks[start:j])
				start = j + 1
			}
		}
	}
	return nil, 0, fmt.Errorf("unbalanced call")
}

func untok(ts []etok) string {
	var b strings.Builder
	for _, t := range ts {
		switch t.kind {
		case "ident":
			b.WriteString("`" + t.text + "` ")
		case "str":
			b.WriteString("'" + strings.ReplaceAll(t.text, "'", "''") + "' ")
		default:
			b.WriteString(t.text + " ")
		}
	}
	return b.String()
}

func translateCall(fn string, args [][]etok) (string, error) {
	tr := func(i int) (string, error) { return TranslateExpr(untok(args[i])) }
	switch {
	case fn == "json_valid" && len(args) == 1:
		a, err := tr(0)
		return "(" + a + " IS JSON)", err
	case fn == "ifnull" && len(args) == 2:
		a, err := tr(0)
		if err != nil {
			return "", err
		}
		c, err := tr(1)
		return "COALESCE(" + a + ", " + c + ")", err
	case fn == "date_format" && len(args) == 2 && len(args[1]) == 1 && args[1][0].kind == "str" && args[1][0].text == "%Y-%m-%d":
		a, err := tr(0)
		return "dbmigrate.date_iso(" + a + ")", err
	case (fn == "char_length" || fn == "octet_length") && len(args) == 1:
		a, err := tr(0)
		return fn + "(" + a + ")", err
	case fn == "length" && len(args) == 1: // MySQL LENGTH is bytes
		a, err := tr(0)
		return "octet_length(" + a + ")", err
	}
	return "", fmt.Errorf("unsupported function %s/%d", fn, len(args))
}

// TranslateDefault maps COLUMN_DEFAULT for a column of kind k.
func TranslateDefault(c Column, k Kind) (string, error) {
	if c.Default == nil {
		return "", nil
	}
	d := *c.Default
	if strings.Contains(c.Extra, "DEFAULT_GENERATED") {
		up := strings.ToUpper(strings.TrimSpace(d))
		if strings.HasPrefix(up, "CURRENT_TIMESTAMP") || strings.HasPrefix(up, "NOW(") {
			// repair-time1: MySQL CURRENT_TIMESTAMP(fsp) is the STATEMENT start
			// truncated to fsp; PG CURRENT_TIMESTAMP is the transaction start and a
			// timestamp(p) column ROUNDS it (fsp 0 could land up to 0.5s in the
			// future). Use statement_timestamp() truncated to the column fsp.
			fsp := int64(0)
			if c.DTPrec != nil {
				fsp = *c.DTPrec
			}
			e, err := pgClockExpr(fsp)
			if err != nil {
				return "", err
			}
			switch k {
			case KTS:
				return e, nil
			case KDatetime:
				return "(" + e + " AT TIME ZONE 'UTC')", nil // source server is UTC
			}
		}
		return "", fmt.Errorf("unsupported default expression %q", d)
	}
	switch k {
	case KInt, KDec, KFloat, KDouble:
		if _, err := Canonical(k, 0, d); err != nil {
			if k == KDec {
				return d, nil
			}
			return "", fmt.Errorf("bad numeric default")
		}
		return d, nil
	case KDate, KTime, KDatetime, KTS:
		if _, err := Canonical(k, 0, d); err != nil {
			return "", fmt.Errorf("zero or invalid temporal default")
		}
		if k == KTS {
			return lit(d + "+00"), nil
		}
		return lit(d), nil
	case KText, KChar, KEnum:
		return lit(d), nil
	}
	return "", fmt.Errorf("unsupported literal default for %s", k)
}

// pgClockExpr is statement_timestamp() truncated to a MySQL fsp (0, 3 or 6);
// other fsp values are refused (no observed source column, no guessed unit).
func pgClockExpr(fsp int64) (string, error) {
	switch fsp {
	case 0:
		return "date_trunc('second', statement_timestamp())", nil
	case 3:
		return "date_trunc('milliseconds', statement_timestamp())", nil
	case 6:
		return "statement_timestamp()", nil
	}
	return "", fmt.Errorf("unsupported clock fsp %d", fsp)
}
