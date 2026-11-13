package api

import "bytes"

// jsonHasNULEscape reports whether encoded JSON carries U+0000. encoding/json
// always writes it as the escape \u0000 (never a raw byte), so a byte scan
// for that escape not itself escaped is exact. types_nul_json_v1.
func jsonHasNULEscape(raw []byte) bool {
	for i := 0; ; {
		j := bytes.Index(raw[i:], []byte("\\u0000"))
		if j < 0 {
			return false
		}
		k := i + j
		bs := 0
		for m := k - 1; m >= 0 && raw[m] == '\\'; m-- {
			bs++
		}
		if bs%2 == 0 {
			return true
		}
		i = k + 1
	}
}
