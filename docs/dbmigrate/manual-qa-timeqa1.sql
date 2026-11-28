-- MANUAL scratch QA (repair-timeqa1). Plain SQL for human inspection; no assertions, no DO blocks, no fixtures.
-- Run in the isolated scratch only, in ONE session (psql). Everything ends in ROLLBACK: no data or schema change persists.
-- Uses existing rows; touches only restaurant_areas.bg_color / pos_visits.covers, then rolls back.
BEGIN;
SET LOCAL search_path = public;
CREATE OR REPLACE FUNCTION dbmigrate.on_update_ts() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  a text; c text; kind text; fsp text; ts timestamptz; g name; explicit text;
  o jsonb := to_jsonb(OLD); n jsonb := to_jsonb(NEW); patch jsonb := '{}'::jsonb;
BEGIN
  explicit := COALESCE(current_setting('dbmigrate.onupd_explicit', true), '');
  PERFORM set_config('dbmigrate.onupd_explicit', '', true);
  -- Fail closed: a mark from another table means a mark was not consumed by
  -- its own refresh (e.g. a foreign BEFORE trigger returned NULL in between).
  IF explicit <> '' AND EXISTS (SELECT 1 FROM unnest(string_to_array(explicit, ',')) m WHERE split_part(m, ':', 1) <> TG_RELID::text) THEN
    RAISE EXCEPTION 'dbmigrate.on_update_ts: stale explicit-column mark % (unconsumed mark trigger)', explicit;
  END IF;
  FOR g IN SELECT attname FROM pg_attribute WHERE attrelid = TG_RELID AND attnum > 0 AND NOT attisdropped AND attgenerated <> '' LOOP
    o := o - g::text; n := n - g::text;
  END LOOP;
  FOREACH a IN ARRAY TG_ARGV LOOP
    c := split_part(ltrim(a, '-'), ':', 1);
    o := o - c; n := n - c;
  END LOOP;
  IF o IS NOT DISTINCT FROM n THEN RETURN NEW; END IF;
  FOREACH a IN ARRAY TG_ARGV LOOP
    CONTINUE WHEN left(a, 1) = '-';
    c := split_part(a, ':', 1); kind := split_part(a, ':', 2); fsp := split_part(a, ':', 3);
    CONTINUE WHEN position(',' || TG_RELID::text || ':' || c || ',' IN ',' || explicit || ',') > 0;
    ts := CASE fsp WHEN '0' THEN date_trunc('second', statement_timestamp()) WHEN '3' THEN date_trunc('milliseconds', statement_timestamp()) WHEN '6' THEN statement_timestamp() END;
    IF ts IS NULL THEN RAISE EXCEPTION 'dbmigrate.on_update_ts: unsupported fsp % for %', fsp, c; END IF;
    IF (to_jsonb(NEW) -> c) IS NOT DISTINCT FROM (to_jsonb(OLD) -> c) THEN
      patch := patch || jsonb_build_object(c, CASE WHEN kind = 'tz' THEN to_jsonb(ts) ELSE to_jsonb(ts AT TIME ZONE 'UTC') END);
    END IF;
  END LOOP;
  IF patch <> '{}'::jsonb THEN NEW := jsonb_populate_record(NEW, patch); END IF;
  RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION dbmigrate.on_update_mark() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur text := COALESCE(current_setting('dbmigrate.onupd_explicit', true), '');
BEGIN
  PERFORM set_config('dbmigrate.onupd_explicit', CASE WHEN cur = '' THEN '' ELSE cur || ',' END || TG_RELID::text || ':' || TG_ARGV[0], true);
  RETURN NEW;
END $$;
DROP TRIGGER "onupd__pos_visits" ON "pos_visits";
DROP TRIGGER "onupd__restaurant_areas" ON "restaurant_areas";
CREATE TRIGGER "onupc__pos_visits__updated_at" BEFORE UPDATE OF "updated_at" ON "pos_visits" FOR EACH ROW EXECUTE FUNCTION dbmigrate.on_update_mark('updated_at');
CREATE TRIGGER "onupd__pos_visits" BEFORE UPDATE ON "pos_visits" FOR EACH ROW EXECUTE FUNCTION dbmigrate.on_update_ts('updated_at:tz:0', '-open_table_id');
CREATE TRIGGER "onupc__restaurant_areas__updated_at" BEFORE UPDATE OF "updated_at" ON "restaurant_areas" FOR EACH ROW EXECUTE FUNCTION dbmigrate.on_update_mark('updated_at');
CREATE TRIGGER "onupd__restaurant_areas" BEFORE UPDATE ON "restaurant_areas" FOR EACH ROW EXECUTE FUNCTION dbmigrate.on_update_ts('updated_at:tz:0');
-- 0. baseline + trigger order on the QA tables (expect onupc… before onupd…, no other BEFORE UPDATE row triggers)
SELECT c.relname, t.tgname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relname IN ('restaurant_areas','pos_visits') AND NOT t.tgisinternal ORDER BY 1,2;
SELECT id, bg_color, updated_at, statement_timestamp() FROM restaurant_areas WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT pg_sleep(1.2);
-- 1. no-op (other column set to itself): expect updated_at UNCHANGED
UPDATE restaurant_areas SET bg_color = bg_color WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT id, updated_at, current_setting('dbmigrate.onupd_explicit', true) AS mark_left FROM restaurant_areas WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT pg_sleep(1.2);
-- 2. real change of another column: expect updated_at = this statement second (truncated)
UPDATE restaurant_areas SET bg_color = COALESCE(bg_color,'') || 'x' WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT id, updated_at, statement_timestamp(), current_setting('dbmigrate.onupd_explicit', true) AS mark_left FROM restaurant_areas WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT pg_sleep(1.2);
-- 3. explicit SAME timestamp + other change: MySQL keeps the timestamp -> expect UNCHANGED vs step 2
UPDATE restaurant_areas SET bg_color = bg_color || 'y', updated_at = updated_at WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT id, updated_at, current_setting('dbmigrate.onupd_explicit', true) AS mark_left FROM restaurant_areas WHERE id = (SELECT min(id) FROM restaurant_areas);
-- 4. explicit DIFFERENT timestamp: expect exactly 2001-02-03 04:05:06+00
UPDATE restaurant_areas SET updated_at = '2001-02-03 04:05:06+00' WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT id, updated_at FROM restaurant_areas WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT pg_sleep(1.2);
-- 5. multi-row: 3 real rows, one statement: expect all three = same statement second
UPDATE restaurant_areas SET bg_color = COALESCE(bg_color,'') || 'z' WHERE id IN (SELECT id FROM restaurant_areas ORDER BY id LIMIT 3);
SELECT id, updated_at FROM restaurant_areas WHERE id IN (SELECT id FROM restaurant_areas ORDER BY id LIMIT 3) ORDER BY id;
-- 6. multi-row with explicit timestamp on all rows (mark per row): expect all = 2002-01-01 00:00:00+00
UPDATE restaurant_areas SET bg_color = bg_color || 'w', updated_at = '2002-01-01 00:00:00+00' WHERE id IN (SELECT id FROM restaurant_areas ORDER BY id LIMIT 3);
SELECT id, updated_at, current_setting('dbmigrate.onupd_explicit', true) AS mark_left FROM restaurant_areas WHERE id IN (SELECT id FROM restaurant_areas ORDER BY id LIMIT 3) ORDER BY id;
SELECT pg_sleep(1.2);
-- 7. UPDATE … FROM with duplicate join rows (each target row updated once): expect refreshed once, mark empty after
UPDATE restaurant_areas a SET bg_color = a.bg_color || 'v' FROM (SELECT (SELECT min(id) FROM restaurant_areas) AS id UNION ALL SELECT (SELECT min(id) FROM restaurant_areas)) d WHERE a.id = d.id;
SELECT id, updated_at, current_setting('dbmigrate.onupd_explicit', true) AS mark_left FROM restaurant_areas WHERE id = (SELECT min(id) FROM restaurant_areas);
SELECT pg_sleep(1.2);
-- 8. generated column table (pos_visits.open_table_id GENERATED): no-op must NOT refresh; real change must refresh
SELECT id, covers, open_table_id, updated_at FROM pos_visits WHERE id = (SELECT min(id) FROM pos_visits);
UPDATE pos_visits SET covers = covers WHERE id = (SELECT min(id) FROM pos_visits);
SELECT id, updated_at FROM pos_visits WHERE id = (SELECT min(id) FROM pos_visits);
UPDATE pos_visits SET covers = covers + 1 WHERE id = (SELECT min(id) FROM pos_visits);
SELECT id, covers, updated_at, statement_timestamp() FROM pos_visits WHERE id = (SELECT min(id) FROM pos_visits);
-- 9. savepoint: mark set inside a rolled-back subtransaction must not survive (expect mark_left empty/NULL)
SAVEPOINT s1;
UPDATE restaurant_areas SET updated_at = updated_at WHERE id = (SELECT min(id) FROM restaurant_areas);
ROLLBACK TO SAVEPOINT s1;
SELECT current_setting('dbmigrate.onupd_explicit', true) AS mark_left;
ROLLBACK;
