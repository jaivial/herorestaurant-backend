#!/usr/bin/env python3
"""gensql.py SCHEMA.json KIND - SQL generated from a dbmigrate schema.json.
KIND: independent  -> MySQL: one JSON line per row {"table":..,"row":[HEX(CAST(col AS BINARY))..]}
      count-mysql  -> MySQL: "<table>\t<count>" per table
      count-pg     -> PostgreSQL: "<table>\t<count>" per table
Used by rehearsal-desktop.sh and 30-import.sh (pg_cutover_v1). No data, no secrets."""
import json, sys
s = json.load(open(sys.argv[1])); kind = sys.argv[2]
BT = chr(96)
if kind != "count-pg":
    print("SET SESSION time_zone='+00:00';")  # same session time zone as the export
for t in sorted(s["tables"], key=lambda t: t["name"]):
    n = t["name"]
    if kind == "independent":
        cols = ",".join("HEX(CAST(%s%s%s AS BINARY))" % (BT, c["name"], BT) for c in t["columns"])
        print("SELECT CONCAT('{\"table\":\"%s\",\"row\":', JSON_ARRAY(%s), '}') FROM %s%s%s;" % (n, cols, BT, n, BT))
    elif kind == "count-mysql":
        print("SELECT '%s', COUNT(*) FROM %s%s%s;" % (n, BT, n, BT))
    elif kind == "count-pg":
        print("SELECT '%s' || chr(9) || count(*) FROM public.\"%s\";" % (n, n))
    else:
        sys.exit("unknown kind")
