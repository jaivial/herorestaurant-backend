# U1: app.norm vs MySQL collations (R5 data + probe pairs)

Date 2026-11-13. Scratch only (padqa2, MySQL source twin read-only). No production.

## 1. Data audit (R5 dump)
For the 117 originally unique indexes that contain text columns (121 listed in `ci-ai-retire-vitess28.tsv`, 4 have no text column), the number of distinct key groups was computed (a) with the current ICU `ci_ai` equality and (b) with `app_norm(col)` on each text column:
- 117 indexes, 8,954 rows scanned, 0 errors, **0 indexes with a different group count**. Switching to app.norm creates no new duplicate and hides none in the existing data.

## 2. Probe pairs (38 pairs) against MySQL 8.0.45
Columns: MySQL utf8mb4_unicode_ci (what most unique text columns use), MySQL utf8mb4_general_ci (the minority), the previous ICU emulation (ci_ai), app_norm v1 (lower+unaccent+rtrim) and app.norm (v2, adds NFKC and zero-width removal). "=" means equal.

| pair | unicode_ci | general_ci | ICU ci_ai | v1 | app.norm |
|---|---|---|---|---|---|
| ß / ss | = | ! | = | = | = |
| Œ / OE, ǆ / dž, ﬁ / fi, ﬀ / ff, ǈ / lj, ŉ / 'n, fullwidth a / a | = | ! | = | = | = |
| ñ/n, ü/u, ç/c, é/e, ÁÉÍ/aei, ё/е, α/ά, İ/i, nbsp/space | = | = | = | = | = |
| combining e + U+0301 / é | = | ! | = | = | = |
| trailing spaces (1 or 2) | = | = | **!** (needs rtrim) | = | = |
| leading space, tab, й/и, Ø/OE, soft hyphen | ! | ! | ! (soft hyphen =) | ! | ! |
| zero-width space / none | = | ! | = | ! | = |
| ℌ / h, ² / 2, ½ / 1⁄2 | = | ! | = | ! | = |
| Æ/AE, æ/ae | **!** | ! | = | = | = (over-folds) |
| Ø / O, Đ / D, Ł / L | **!** | ! | = | = | = (over-folds) |
| ı / i | ! | = | ! | = | = (over-folds) |
| ʼ (modifier apostrophe) / ' | ! | ! | ! | = | = (over-folds) |
| emoji 😀 / 😁 | = (MySQL quirk: all supplementary chars equal) | = | ! | ! | ! (better than MySQL) |

## 3. Reading
- app.norm matches utf8mb4_unicode_ci on every Spanish/Latin case that appears in a restaurant (ß/ss, tildes, case, combining marks, trailing spaces, NFKC compatibility forms).
- It over-folds Æ/æ, Ø, Đ, Ł, ı and ʼ: MySQL treats them as different; app.norm treats them as the same as their base letters. Effect: a few more "duplicate" rejections for names that differ only by those letters. Safe direction (no silent duplicate), 0 occurrences in R5 data.
- Where MySQL and app.norm differ in the other direction there is only the emoji case (app.norm distinguishes 😀 and 😁, MySQL does not): better, not a regression risk. Soft hyphen: both treat it as different; the old ICU emulation treated it as equal.
- general_ci columns (a minority, e.g. session ids, codes): MySQL general_ci is stricter than unicode_ci on ß/ligatures, so app.norm treats more pairs as equal there. For those columns (hex ids, tokens, idempotency keys, emails) use app.norm_ascii or no folding; the DDL generator should pick per column from `mysql_collation` (general_ci -> norm_ascii unless the column holds free text).
- The previous ICU emulation (ci_ai) differed from MySQL on Æ/æ, Ø, Đ, Ł, soft hyphen and trailing spaces. app.norm is closer to MySQL than it was.

Function: `docs/pgnative/app-norm.sql`.
