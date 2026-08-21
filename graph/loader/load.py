"""Load triples.jsonl into Neo4j.

Idempotent by construction: every write is a MERGE on the node's unique key
(see graph/schema.cypher), so re-indexing after a code change updates facts in
place instead of duplicating them. Nothing here is ever CREATEd blindly and
nothing is deleted except stale edges for files that were re-extracted.

Usage:
    python loader/load.py --triples out/triples.jsonl [--reset]
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import sys
import time
from collections import defaultdict

from dotenv import load_dotenv
from neo4j import GraphDatabase

GRAPH_DIR = pathlib.Path(__file__).resolve().parent.parent
load_dotenv(GRAPH_DIR / ".env")

BATCH = 5_000

# Relationship types the loader is allowed to write. Cypher cannot parameterise
# a relationship type, so it is interpolated -- this allowlist is what keeps
# that safe from injection via a malformed triples file.
ALLOWED_EDGES = {
    "IN", "IMPORTS", "DECLARES", "CALLS", "HANDLED_BY",
    "READS", "WRITES", "USES_ENV", "RENDERS", "FETCHES", "MOUNTS",
    "SERVED_BY",
}
ALLOWED_LABELS = {
    "Repo", "File", "Package", "Func", "Type", "Endpoint",
    "Table", "Column", "EnvVar", "Doc", "Component", "Route",
}

# Unique key property per label, matching the constraints in schema.cypher.
KEY_PROP = {
    "Repo": "name",
    "File": "path",
    "Package": "importPath",
    "Func": "key",
    "Type": "key",
    "Endpoint": "key",
    "Table": "name",
    "Column": "key",
    "EnvVar": "name",
    "Doc": "key",
    "Component": "key",
    "Route": "key",
}


def read_triples(paths: list[pathlib.Path]):
    nodes: dict[str, list[dict]] = defaultdict(list)
    edges: dict[tuple[str, str, str], list[dict]] = defaultdict(list)
    bad = 0
    for path in paths:
        with path.open() as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    d = json.loads(line)
                except json.JSONDecodeError:
                    bad += 1
                    continue
                if d.get("kind") == "node":
                    label = d["label"]
                    if label not in ALLOWED_LABELS:
                        bad += 1
                        continue
                    props = dict(d.get("props") or {})
                    props[KEY_PROP[label]] = d["key"]
                    nodes[label].append(props)
                elif d.get("kind") == "edge":
                    t, fl, tl = d["type"], d["fromLabel"], d["toLabel"]
                    if (
                        t not in ALLOWED_EDGES
                        or fl not in ALLOWED_LABELS
                        or tl not in ALLOWED_LABELS
                    ):
                        bad += 1
                        continue
                    edges[(t, fl, tl)].append(
                        {"from": d["fromKey"], "to": d["toKey"], "props": d.get("props") or {}}
                    )
    return nodes, edges, bad


def chunks(seq, n=BATCH):
    for i in range(0, len(seq), n):
        yield seq[i : i + n]


def load(session, nodes, edges):
    counts = {"nodes": 0, "edges": 0}

    for label, rows in nodes.items():
        kp = KEY_PROP[label]
        # SET += merges the property map, so re-runs refresh line numbers etc.
        q = (
            f"UNWIND $rows AS row "
            f"MERGE (n:{label} {{{kp}: row.{kp}}}) "
            f"SET n += row"
        )
        for part in chunks(rows):
            session.run(q, rows=part)
            counts["nodes"] += len(part)
        print(f"  {label:<10} {len(rows):>6}", file=sys.stderr)

    for (etype, fl, tl), rows in edges.items():
        fkp, tkp = KEY_PROP[fl], KEY_PROP[tl]
        # MATCH (not MERGE) the endpoints: if a node is missing the edge is
        # skipped rather than conjuring a half-empty node.
        q = (
            f"UNWIND $rows AS row "
            f"MATCH (a:{fl} {{{fkp}: row.from}}) "
            f"MATCH (b:{tl} {{{tkp}: row.to}}) "
            f"MERGE (a)-[r:{etype}]->(b) "
            f"SET r += row.props"
        )
        for part in chunks(rows):
            session.run(q, rows=part)
            counts["edges"] += len(part)
        print(f"  {etype:<10} {fl}->{tl} {len(rows):>6}", file=sys.stderr)

    return counts


# Relationship types the loader is allowed to write. Cypher cannot parameterise
# a relationship type, so it is interpolated -- the ALLOWED_EDGES allowlist is
# what keeps that safe from injection via a malformed triples file.
LINK_STEPS = [
    # 1. Give every endpoint the parameter-name-insensitive form used to join
    #    the two sides of the stack. The Go extractor emits `{id}`, the TS one
    #    `{param}`; both collapse to `{}`.
    (
        "canonical paths",
        """
        MATCH (e:Endpoint) WHERE e.path IS NOT NULL
        WITH e, '/' + reduce(acc = '', seg IN [
                x IN split(e.path, '/') WHERE x <> ''
            ] | acc + CASE WHEN acc = '' THEN '' ELSE '/' END +
                CASE WHEN seg STARTS WITH '{' THEN '{}' ELSE seg END) AS c
        SET e.canon = CASE WHEN c = '/' THEN '/' ELSE c END
        """,
    ),
    # 2. Link a frontend call to the backend route that serves it. Frontend-only
    #    endpoints (no CALLS_ENDPOINT edge) are exactly the dead or unimplemented
    #    calls worth reporting.
    (
        "frontend -> backend endpoint links",
        """
        MATCH (fe:Endpoint) WHERE fe.calledFrom IS NOT NULL
        MATCH (be:Endpoint)
          WHERE be.calledFrom IS NULL AND be.method = fe.method AND be.canon = fe.canon
        MERGE (fe)-[:SERVED_BY]->(be)
        """,
    ),
    # 3. Same, but where the backend declares a wildcard segment the frontend
    #    hardcodes: `/comida/{tipo}/{id}/image` serves the literal
    #    `/comida/platos/{id}/image`. Matching segment-wise lets a `{}` on the
    #    backend side absorb any literal, which plain equality cannot do.
    (
        "wildcard endpoint links",
        """
        MATCH (fe:Endpoint) WHERE fe.calledFrom IS NOT NULL
          AND NOT exists((fe)-[:SERVED_BY]->()) AND NOT exists((fe)-[:HANDLED_BY]->())
        WITH fe, [x IN split(fe.canon, '/') WHERE x <> ''] AS fseg
        MATCH (be:Endpoint)
          WHERE be.calledFrom IS NULL AND be.method = fe.method
        WITH fe, fseg, be, [x IN split(be.canon, '/') WHERE x <> ''] AS bseg
        WHERE size(fseg) = size(bseg)
          AND all(i IN range(0, size(fseg) - 1)
                  WHERE bseg[i] = '{}' OR bseg[i] = fseg[i])
        MERGE (fe)-[:SERVED_BY {viaWildcard: true}]->(be)
        """,
    ),
]


def link(session) -> None:
    """Derive cross-repo edges that no single extractor can see.

    Each extractor knows only its own repo. The join between a frontend fetch
    and the Go route that answers it has to happen here, once both sides are
    present.
    """
    for label, q in LINK_STEPS:
        res = session.run(q).consume()
        print(
            f"  {label:<38} +{res.counters.properties_set} props "
            f"+{res.counters.relationships_created} rels",
            file=sys.stderr,
        )


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--triples",
        nargs="+",
        default=[str(GRAPH_DIR / "out" / "triples.jsonl")],
        help="one or more triples.jsonl files (one per repo)",
    )
    ap.add_argument("--reset", action="store_true", help="delete all graph data first")
    args = ap.parse_args()

    paths = [pathlib.Path(p) for p in args.triples]
    missing = [p for p in paths if not p.exists()]
    if missing:
        for p in missing:
            print(f"load: {p} not found (run the extractor first)", file=sys.stderr)
        return 1

    uri = os.environ.get("NEO4J_URI", "bolt://127.0.0.1:7687")
    user = os.environ.get("NEO4J_USER", "neo4j")
    pw = os.environ.get("NEO4J_PASSWORD")
    if not pw:
        print("load: NEO4J_PASSWORD not set (copy graph/.env.example)", file=sys.stderr)
        return 1

    nodes, edges, bad = read_triples(paths)
    total_n = sum(len(v) for v in nodes.values())
    total_e = sum(len(v) for v in edges.values())
    print(f"parsed {total_n} nodes / {total_e} edges (skipped {bad})", file=sys.stderr)

    t0 = time.time()
    with GraphDatabase.driver(uri, auth=(user, pw)) as driver:
        driver.verify_connectivity()
        with driver.session() as session:
            if args.reset:
                print("reset: deleting existing data", file=sys.stderr)
                session.run("MATCH (n) CALL (n) { DETACH DELETE n } IN TRANSACTIONS OF 10000 ROWS")
            counts = load(session, nodes, edges)
            print("linking:", file=sys.stderr)
            link(session)
            stats = session.run(
                "MATCH (n) WITH count(n) AS nodes "
                "MATCH ()-[r]->() RETURN nodes, count(r) AS rels"
            ).single()

    print(
        f"loaded {counts['nodes']} nodes / {counts['edges']} edges "
        f"in {time.time() - t0:.1f}s -> graph now has "
        f"{stats['nodes']} nodes / {stats['rels']} rels",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
