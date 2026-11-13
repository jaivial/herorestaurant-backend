package dbmigrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind is the logical value class of a source column; it drives the PG
// type, the canonical text form and the digest. Unknown types fail closed.
type Kind string

const (
	KInt      Kind = "int"
	KDec      Kind = "decimal"
	KFloat    Kind = "float"
	KDouble   Kind = "double"
	KText     Kind = "text"
	KChar     Kind = "char"
	KEnum     Kind = "enum"
	KDate     Kind = "date"
	KTime     Kind = "time"
	KDatetime Kind = "datetime"
	KTS       Kind = "timestamp"
	KJSON     Kind = "json"
	KBinary   Kind = "binary"
)

// ColumnKind classifies a column or returns an unsupported-feature error.
func ColumnKind(c Column) (Kind, error) {
	switch c.DataType {
	case "tinyint", "smallint", "mediumint", "int", "bigint":
		return KInt, nil
	case "decimal":
		return KDec, nil
	case "float":
		return KFloat, nil
	case "double":
		return KDouble, nil
	case "varchar", "tinytext", "text", "mediumtext", "longtext":
		return KText, nil
	case "char":
		return KChar, nil
	case "enum":
		return KEnum, nil
	case "date":
		return KDate, nil
	case "time":
		return KTime, nil
	case "datetime":
		return KDatetime, nil
	case "timestamp":
		return KTS, nil
	case "json":
		return KJSON, nil
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return KBinary, nil
	}
	return "", fmt.Errorf("unsupported column type %s (%s)", c.DataType, c.ColType)
}

// Canonical returns the dialect-independent text of one non-NULL value read
// as text (binary already hex). Both the MySQL export and the PG re-read go
// through it, so equal canonical text means equal value.
func Canonical(k Kind, scale int, raw string) (string, error) {
	switch k {
	case KInt:
		n, ok := new(big.Int).SetString(strings.TrimPrefix(raw, "+"), 10)
		if !ok {
			return "", fmt.Errorf("not an integer")
		}
		return n.String(), nil
	case KDec:
		r, ok := new(big.Rat).SetString(raw)
		if !ok {
			return "", fmt.Errorf("not a decimal")
		}
		return r.FloatString(scale), nil
	case KFloat, KDouble:
		bits := 64
		if k == KFloat {
			bits = 32
		}
		f, err := strconv.ParseFloat(raw, bits)
		if err != nil {
			return "", err
		}
		return strconv.FormatFloat(f, 'g', -1, bits), nil
	case KChar:
		return strings.TrimRight(raw, " "), nil
	case KText, KEnum:
		return raw, nil
	case KDate:
		t, err := time.Parse("2006-01-02", raw)
		if err != nil || t.Year() < 1 {
			return "", fmt.Errorf("invalid or zero date")
		}
		return t.Format("2006-01-02"), nil
	case KTime:
		return canonClock(raw)
	case KDatetime, KTS:
		d, c, ok := strings.Cut(raw, " ")
		if !ok {
			return "", fmt.Errorf("invalid datetime")
		}
		if _, err := Canonical(KDate, 0, d); err != nil {
			return "", err
		}
		cc, err := canonClock(c)
		if err != nil {
			return "", err
		}
		return d + " " + cc, nil
	case KJSON:
		return CanonicalJSON(raw)
	case KBinary:
		return strings.ToLower(raw), nil
	}
	return "", fmt.Errorf("no canonical form for %s", k)
}

// canonClock: HH:MM:SS with exactly 6 fractional digits; PG TIME range only.
func canonClock(raw string) (string, error) {
	hms, frac, _ := strings.Cut(raw, ".")
	p := strings.Split(hms, ":")
	if len(p) != 3 || len(p[0]) != 2 || len(p[1]) != 2 || len(p[2]) != 2 || len(frac) > 6 {
		return "", fmt.Errorf("time out of PG range or malformed")
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	s, e3 := strconv.Atoi(p[2])
	if e1 != nil || e2 != nil || e3 != nil || h > 23 || m > 59 || s > 59 {
		return "", fmt.Errorf("time out of PG range or malformed")
	}
	for _, r := range frac {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("malformed fraction")
		}
	}
	return hms + "." + frac + strings.Repeat("0", 6-len(frac)), nil
}

// CanonicalJSON decodes with json.Number (no float64 rounding of large
// integers) and re-encodes with sorted keys and no HTML escaping.
func CanonicalJSON(raw string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("invalid json")
	}
	if dec.More() {
		return "", fmt.Errorf("trailing json data")
	}
	var b bytes.Buffer
	if err := writeJSON(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeJSON(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeStr(b, k)
			b.WriteByte(':')
			if err := writeJSON(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case string:
		writeStr(b, x)
	case json.Number:
		// Numeric identity: 1.50 and 1.5 are the same JSON number.
		r, ok := new(big.Rat).SetString(x.String())
		if !ok {
			return fmt.Errorf("bad json number")
		}
		b.WriteString(r.RatString())
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case nil:
		b.WriteString("null")
	default:
		return fmt.Errorf("unexpected json value")
	}
	return nil
}

func writeStr(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Truncate(b.Len() - 1) // Encode appends '\n'
}

// RowHash: sha256 over length-prefixed canonical values, NULL apart from "".
func RowHash(vals []*string) string {
	h := sha256.New()
	for _, v := range vals {
		if v == nil {
			h.Write([]byte("N;"))
			continue
		}
		fmt.Fprintf(h, "V%d:%s;", len(*v), *v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TableDigest: sha256 of sorted row hashes (order/collation independent).
func TableDigest(rowHashes []string) string {
	s := append([]string(nil), rowHashes...)
	sort.Strings(s)
	h := sha256.New()
	for _, x := range s {
		h.Write([]byte(x))
	}
	return hex.EncodeToString(h.Sum(nil))
}
