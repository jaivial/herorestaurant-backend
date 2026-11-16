# result-native-pos7 — POS checkpoint 7: fiscal + recall (31 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No fiscal request, number consumption, payment, SQL or external effect.

## Scope (2 files complete)
| File | Sites |
|---|---|
| `backoffice_pos_fiscal.go` | 23 (1 dynamic) |
| `backoffice_pos_recall.go` | 8 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r62, run from the repo root:
  - Canonical 2475 = native 1991 (includes 4 provisional) + blocked 3 + unwrapped 481.
  - Dynamic 78; audit failures 0.
- POS: 200/564 ported, 364 remaining (36 dynamic), 14/35 files done.
- Lexical check: one false positive. The audit action literal `'DUPLICATE'` matched the `DUPLICATE KEY` pattern.

## Real delta found and handled: CHAR(64) hash padding
- PG `bpchar` keeps trailing padding on output; MySQL `CHAR` strips it.
- The duplicate-copy path stores `COALESCE(previous_hash, '')`. That is `''` on MySQL but 64 spaces on PG.
- Every PG read of `last_hash`, `content_hash` and `previous_hash` now uses `CAST(... AS text)`, which strips the padding. The hash chain therefore compares the same strings on both engines.
- The storage expression itself is unchanged, to stay source-faithful.

## Semantics (compile-only)
### Series
- `INSERT IGNORE` becomes `ON CONFLICT (restaurant_id, terminal_key, series_type) DO NOTHING`, the only collidable key.
- IGNORE's other warning effects are not reproduced.
- `next_number` is `numeric(20,0)` (unsigned range) and is scanned into int64 on both engines.

### Documents
- The insert uses `InsertID`.
- `issuedAtStored` is already UTC and truncated to the second, which suits the naive column.
- The duplicate copy (`INSERT…SELECT`) uses `InsertSelectID`: no source row gives id 0, as on MySQL. The select-list parameters are typed.
- The unique numbering keys are unchanged, and the number is consumed by an UPDATE in the same transaction.

### Chain verification
- `issued_at` is read as a UTC `time.Time`; `series_number` as int64.
- The JSON columns are text with identical bytes; enums are read as text; hashes are de-padded.
- This is a source-review claim only. Runtime QA is critical here, because the fiscal chain is a cutover gate.

### Audit
- The canonical JSON string is stored as jsonb, which normalises it. This applies to the audit only, not to the hash chain.
- The duplicate audit uses `jsonb_build_object` with typed arguments.

### Ignored reads inside the transaction
- The visit-customer read and the existing-document read run in savepoints (`ErrNoRows` excluded).

### VAT breakdown
- `vat_rate_snapshot` is cast to text; keys such as `'21.00'` are identical to the MySQL DECIMAL bytes.

### Recall
- **Duplicate skip:** the source skips a duplicate-key insert and continues the transaction. On PG, each line insert now runs in `TxStatementScope` so the skip does not abort the transaction.
- New line ids come from `InsertID`.
- `NULLIF(course, '')` gets the PAD SPACE `rtrim` twin.

## Gates (all open)
- **New:**
  - Fiscal hash-chain runtime QA across the cutover; existing documents must re-verify on PG.
  - jsonb normalisation of the fiscal audit.
  - `IGNORE` effects on fiscal series.
- **Repair batch (technical, needs QA):** guest-label `NULLIF`; catalog `EXISTS` scope bug. Parity kept for now.
- **Source anomalies:** the PIN columns (`pos_pin_hash`, `pos_pin_set_at`) are dump-only, not checked live.
- **Truncation:** category 140 → 120 `IGNORE` truncation; suppression `IGNORE` truncation.
- **Carried:**
  - Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
  - Import; NUL / JSON audit drops; gap locks; claim batch size; routing.
  - Search (FTS); TIME range; UUIDs; SEO and multi-key upserts.
  - Numeric and collation.

## Next POS checkpoint: about 30 sites
- `backoffice_pos_kitchen.go` 22 (4 dynamic) + `backoffice_pos_split.go` 8 = 30 sites.

