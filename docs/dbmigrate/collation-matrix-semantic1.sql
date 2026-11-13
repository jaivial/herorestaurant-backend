-- collation-matrix-semantic1.sql (repair-semantic1). MANUAL read-only SELECTs
-- for a human operator. Literal codepoint samples only; no table reads, no user data,
-- no fixtures, no assertions. Run each section on its own engine and record
-- the 0/1 output yourself. Not executed by the worker.

-- ===== MySQL (villacarmen_source, local restore; production may differ) =====
SELECT 'ascii_case' k, _utf8mb4'Mar' = _utf8mb4'mar' COLLATE utf8mb4_unicode_ci u, _utf8mb4'Mar' = _utf8mb4'mar' COLLATE utf8mb4_general_ci g, _utf8mb4'Mar' = _utf8mb4'mar' COLLATE utf8mb4_0900_ai_ci n
UNION ALL SELECT 'accent_a', _utf8mb4 0xC3A1 = _utf8mb4'a' COLLATE utf8mb4_unicode_ci, _utf8mb4 0xC3A1 = _utf8mb4'a' COLLATE utf8mb4_general_ci, _utf8mb4 0xC3A1 = _utf8mb4'a' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'n_tilde', _utf8mb4 0xC3B1 = _utf8mb4'n' COLLATE utf8mb4_unicode_ci, _utf8mb4 0xC3B1 = _utf8mb4'n' COLLATE utf8mb4_general_ci, _utf8mb4 0xC3B1 = _utf8mb4'n' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'sharp_s_ss', _utf8mb4 0xC39F = _utf8mb4'ss' COLLATE utf8mb4_unicode_ci, _utf8mb4 0xC39F = _utf8mb4'ss' COLLATE utf8mb4_general_ci, _utf8mb4 0xC39F = _utf8mb4'ss' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'sharp_s_s', _utf8mb4 0xC39F = _utf8mb4's' COLLATE utf8mb4_unicode_ci, _utf8mb4 0xC39F = _utf8mb4's' COLLATE utf8mb4_general_ci, _utf8mb4 0xC39F = _utf8mb4's' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'trailing_sp', _utf8mb4'main ' = _utf8mb4'main' COLLATE utf8mb4_unicode_ci, _utf8mb4'main ' = _utf8mb4'main' COLLATE utf8mb4_general_ci, _utf8mb4'main ' = _utf8mb4'main' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'trailing_tab', _utf8mb4'main\t' = _utf8mb4'main' COLLATE utf8mb4_unicode_ci, _utf8mb4'main\t' = _utf8mb4'main' COLLATE utf8mb4_general_ci, _utf8mb4'main\t' = _utf8mb4'main' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'nul_embedded', CONCAT(_utf8mb4'a',CHAR(0 USING utf8mb4),_utf8mb4'b') = _utf8mb4'ab' COLLATE utf8mb4_unicode_ci, CONCAT(_utf8mb4'a',CHAR(0 USING utf8mb4),_utf8mb4'b') = _utf8mb4'ab' COLLATE utf8mb4_general_ci, CONCAT(_utf8mb4'a',CHAR(0 USING utf8mb4),_utf8mb4'b') = _utf8mb4'ab' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'combining_acute', _utf8mb4 0x61CC81 = _utf8mb4 0xC3A1 COLLATE utf8mb4_unicode_ci, _utf8mb4 0x61CC81 = _utf8mb4 0xC3A1 COLLATE utf8mb4_general_ci, _utf8mb4 0x61CC81 = _utf8mb4 0xC3A1 COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'ligature_ae', _utf8mb4 0xC3A6 = _utf8mb4'ae' COLLATE utf8mb4_unicode_ci, _utf8mb4 0xC3A6 = _utf8mb4'ae' COLLATE utf8mb4_general_ci, _utf8mb4 0xC3A6 = _utf8mb4'ae' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'like_accent', _utf8mb4 0x6DC3A172 LIKE _utf8mb4'%mar%' COLLATE utf8mb4_unicode_ci, _utf8mb4 0x6DC3A172 LIKE _utf8mb4'%mar%' COLLATE utf8mb4_general_ci, _utf8mb4 0x6DC3A172 LIKE _utf8mb4'%mar%' COLLATE utf8mb4_0900_ai_ci
UNION ALL SELECT 'like_trailing', _utf8mb4'main ' LIKE _utf8mb4'main' COLLATE utf8mb4_unicode_ci, _utf8mb4'main ' LIKE _utf8mb4'main' COLLATE utf8mb4_general_ci, _utf8mb4'main ' LIKE _utf8mb4'main' COLLATE utf8mb4_0900_ai_ci;
-- ENUM numeric/ordinal input semantics: expression-only (no table), read docs + observe:
SELECT CAST(_utf8mb4'2' AS UNSIGNED) note_only;  -- ENUM ordinal behaviour needs a scratch table; NOT included (no fixtures). See doc.

-- ===== PostgreSQL (isolated scratch, dbmigrate.ci_ai) =====
SELECT 'ascii_case' k, ('Mar' COLLATE dbmigrate.ci_ai) = 'mar' ci_ai, (rtrim('Mar',' ') COLLATE dbmigrate.ci_ai) = rtrim('mar',' ') rtrim_ci_ai
UNION ALL SELECT 'accent_a', (U&'\00E1' COLLATE dbmigrate.ci_ai) = 'a', (rtrim(U&'\00E1',' ') COLLATE dbmigrate.ci_ai) = 'a'
UNION ALL SELECT 'n_tilde', (U&'\00F1' COLLATE dbmigrate.ci_ai) = 'n', (rtrim(U&'\00F1',' ') COLLATE dbmigrate.ci_ai) = 'n'
UNION ALL SELECT 'sharp_s_ss', (U&'\00DF' COLLATE dbmigrate.ci_ai) = 'ss', (rtrim(U&'\00DF',' ') COLLATE dbmigrate.ci_ai) = 'ss'
UNION ALL SELECT 'sharp_s_s', (U&'\00DF' COLLATE dbmigrate.ci_ai) = 's', (rtrim(U&'\00DF',' ') COLLATE dbmigrate.ci_ai) = 's'
UNION ALL SELECT 'trailing_sp', ('main ' COLLATE dbmigrate.ci_ai) = 'main', (rtrim('main ',' ') COLLATE dbmigrate.ci_ai) = rtrim('main',' ')
UNION ALL SELECT 'trailing_tab', (E'main\t' COLLATE dbmigrate.ci_ai) = 'main', (rtrim(E'main\t',' ') COLLATE dbmigrate.ci_ai) = 'main'
UNION ALL SELECT 'combining_acute', (U&'a\0301' COLLATE dbmigrate.ci_ai) = U&'\00E1', (rtrim(U&'a\0301',' ') COLLATE dbmigrate.ci_ai) = U&'\00E1'
UNION ALL SELECT 'ligature_ae', (U&'\00E6' COLLATE dbmigrate.ci_ai) = 'ae', (rtrim(U&'\00E6',' ') COLLATE dbmigrate.ci_ai) = 'ae';
-- PG text cannot hold NUL (bytea adapter stores it): nul_embedded is N/A on PG text.
-- LIKE on a nondeterministic collation (PG17 supports it only for some patterns): observe error/result:
SELECT 'like_accent' k, (U&'m\00E1r' COLLATE dbmigrate.ci_ai) LIKE '%mar%' r;
SELECT 'ilike_C' k, (U&'m\00E1r' COLLATE "C") ILIKE '%mar%' r;
-- unaccent is AVAILABLE but NOT installed; do not CREATE EXTENSION here.
