-- PROPOSAL, NOT APPLIED (repair-vitess8). Scratch-only, coordinator review first.
-- Replaces dbmigrate.on_update_ts so <col>__wk columns are ignored when deciding a
-- real change. The 273 triggers keep referencing the function by name (no recreate).
-- Expected pg_proc.prosrc sha256 after apply: fb5d43068b75333aacee0291c3902e296fc5a4eb19ade7c3a968808108bde3eb
-- (on_update_mark unchanged: 9c05aefd03115094f9febf12e21825eec47f7841e1a4e8611f532d003e6359e6)
BEGIN;
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
  -- repair-vitess8: derived weight-key columns (<col>__wk, no MySQL
  -- counterpart) never count as a real change.
  FOR g IN SELECT attname FROM pg_attribute WHERE attrelid = TG_RELID AND attnum > 0 AND NOT attisdropped AND right(attname::text, 4) = '__wk' LOOP
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
ROLLBACK; -- proposal: replace with COMMIT only after review
