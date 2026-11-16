#!/usr/bin/env python3
"""runtime-smoke-onconflict.py - catalog check of every ON CONFLICT target.

PostgreSQL resolves the ON CONFLICT arbiter at PLAN time: a target column list
that matches no non-partial, non-expression unique index (or constraint) fails
with SQLSTATE 42P10 on the FIRST execution, whatever the data. The production
cutover was rolled back for exactly that (bot_inbound_queue, unique index on
app.norm_ascii(message_id)). This script finds every `INSERT INTO <t> ...
ON CONFLICT (<cols>)` literal in the Go sources and checks it against the live
catalog of the scratch DB, so the whole class is covered even where no HTTP
call of the smoke reaches the statement.

Usage: runtime-smoke-onconflict.py <src-root> <psql-conninfo> [schema-map]
Prints TSV: status  file:line  table  target  detail
status: OK | MISSING_ARBITER (=42P10 at runtime) | NO_TABLE | DYNAMIC (target
built at runtime, check by hand). Exit 1 if any MISSING_ARBITER or NO_TABLE.
Read-only (catalog SELECTs only).
"""
import os, re, subprocess, sys

src, conninfo = sys.argv[1], sys.argv[2]
# bot store tables live in schema bot_context, the rest in public.
BOT_TABLES_SCHEMA = "bot_context"

def psql(sql):
    out = subprocess.run(["psql", conninfo, "-AtX", "-F", "\t", "-c", sql],
                         check=True, capture_output=True, text=True).stdout
    return [l.split("\t") for l in out.splitlines() if l]

# (schema, table) -> list of (kind, [cols] or None if expression/partial)
arbiters = {}
def norm_el(e):
    e = re.sub(r"::[a-z ]+(\(\d+\))?", "", e.lower())
    return re.sub(r"[()\s\"]", "", e)

def split_top(s):
    out, depth, cur = [], 0, ""
    for ch in s:
        if ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
        if ch == "," and depth == 0:
            out.append(cur); cur = ""
        else:
            cur += ch
    out.append(cur)
    return [x.strip() for x in out if x.strip()]

for schema, table, idef, expr, pred, name in psql("""
SELECT n.nspname, c.relname,
       substring(pg_get_indexdef(i.indexrelid) from 'USING [a-z]+ \\((.*)\\)( WHERE .*)?$'),
       (i.indexprs IS NOT NULL)::text, (i.indpred IS NOT NULL)::text, ic.relname
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_class ic ON ic.oid = i.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE i.indisunique AND n.nspname IN ('public','bot_context','app','dbmigrate')
"""):
    cols = idef or ""
    arbiters.setdefault((schema, table.lower()), []).append(
        (name, None if pred == "true" else sorted(norm_el(x) for x in split_top(cols)), cols, expr, pred))
tables = {(s, t.lower()) for s, t in psql(
    "SELECT table_schema, table_name FROM information_schema.tables "
    "WHERE table_schema IN ('public','bot_context','app','dbmigrate')")}

lit_re = re.compile(r"`([^`]*)`|\"((?:[^\"\\\n]|\\.)*)\"", re.S)
ins_re = re.compile(r"INSERT\s+INTO\s+([A-Za-z0-9_.\"]+)", re.I)
oc_re = re.compile(r"ON\s+CONFLICT\s*(\(|ON\s+CONSTRAINT\s+(\w+)|DO\b)", re.I)

def balanced(s, i):
    """s[i] == '('; return the text inside the matching parens."""
    depth = 0
    for j in range(i, len(s)):
        depth += s[j] == "("
        depth -= s[j] == ")"
        if depth == 0:
            return s[i + 1:j]
    return None

rows, bad = [], 0
for root, _, files in os.walk(os.path.join(src, "internal")):
    for f in files:
        if not f.endswith(".go") or f.endswith("_test.go"):
            continue
        p = os.path.join(root, f)
        text = open(p, encoding="utf-8", errors="replace").read()
        lits = [(m.start(), m.group(1) if m.group(1) is not None else m.group(2)) for m in lit_re.finditer(text)]
        for idx, (pos, lit) in enumerate(lits):
            for m in oc_re.finditer(lit):
                line = text.count("\n", 0, pos) + lit.count("\n", 0, m.start()) + 1
                rel = os.path.relpath(p, src) + ":" + str(line)
                # table: last INSERT INTO before the ON CONFLICT in this literal,
                # else in the previous 3 literals (concatenated builders).
                # A fragment literal (posUpsert := "... ON CONFLICT ...") is
                # glued to an INSERT literal that may come right AFTER it.
                tbl = None
                found = ins_re.findall(lit[:m.start()])
                if found:
                    tbl = found[-1].strip('"')
                else:
                    near = [lits[j] for j in (idx + 1, idx + 2, idx - 1, idx - 2, idx - 3)
                            if 0 <= j < len(lits) and abs(lits[j][0] - pos) < 900]
                    for _, seg in near:
                        found = ins_re.findall(seg)
                        if found:
                            tbl = found[-1].strip('"')
                            break
                if m.group(1).upper().startswith("DO"):
                    rows.append(("OK", rel, tbl or "?", "(none)", "no target: any constraint"))
                    continue
                if m.group(2):
                    rows.append(("OK" if any(a[0] == m.group(2) for v in arbiters.values() for a in v) else "MISSING_ARBITER",
                                 rel, tbl or "?", "ON CONSTRAINT " + m.group(2), ""))
                    continue
                target = (balanced(lit, m.end() - 1) or "").strip()
                if not tbl or not target or "%" in target or not re.fullmatch(r"[\w\s,\".():]+", target):
                    rows.append(("DYNAMIC", rel, tbl or "?", target, "table or target not literal"))
                    continue
                cols = sorted(norm_el(c) for c in split_top(target))
                if "." in tbl:
                    sch, t = tbl.lower().split(".", 1)
                    cands = [(sch, t)]
                else:
                    cands = [("public", tbl.lower()), (BOT_TABLES_SCHEMA, tbl.lower())]
                hit = [c for c in cands if c in tables]
                if not hit:
                    rows.append(("NO_TABLE", rel, tbl, target, "table not in scratch DB")); bad += 1
                    continue
                okk, detail = False, []
                for key in hit:
                    for name, acols, raw, expr, pred in arbiters.get(key, []):
                        if acols is not None and acols == cols:
                            okk = True
                        detail.append(f"{key[0]}.{name}({raw}{' +expr' if expr=='true' else ''}{' partial' if pred=='true' else ''})")
                if okk:
                    rows.append(("OK", rel, tbl, target, ""))
                else:
                    bad += 1
                    rows.append(("MISSING_ARBITER", rel, tbl, target, "42P10; unique indexes: " + ("; ".join(detail) or "none")))

for r in sorted(rows, key=lambda r: (r[0] != "MISSING_ARBITER", r[0], r[1])):
    print("\t".join(r))
sys.exit(1 if bad else 0)
