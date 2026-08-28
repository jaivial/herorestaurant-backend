"""Stdio MCP server exposing the newvillacarmen code graph to MCP clients
(opencode, freebuff/manicode, Claude). Ports the pi extension's graph_impact
and graph_orphan_calls tools. Reads creds from graph/.env.

Tools:
  graph_schema          labels/rel-types + counts
  graph_query           read-only Cypher (bounded)
  graph_impact          blast radius of a table/func/endpoint/component/file
  graph_orphan_calls    frontend calls with no backend route
"""
from __future__ import annotations

import json
import logging
import os
import re
from pathlib import Path
from typing import Any

from dotenv import load_dotenv
from mcp.server.fastmcp import FastMCP
from neo4j import GraphDatabase

GRAPH_DIR = Path(__file__).resolve().parent
load_dotenv(GRAPH_DIR / ".env")

NEO4J_URI = os.getenv("NEO4J_URI", "bolt://127.0.0.1:7687")
NEO4J_USER = os.getenv("NEO4J_USER", "neo4j")
NEO4J_PASSWORD = os.getenv("NEO4J_PASSWORD")
NEO4J_DATABASE = os.getenv("NEO4J_DATABASE", "neo4j")

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("nvc-graph-mcp")
_driver = None

if not NEO4J_PASSWORD:
    raise SystemExit("NEO4J_PASSWORD missing from graph/.env")


def _driver_get():
    global _driver
    if _driver is None:
        _driver = GraphDatabase.driver(NEO4J_URI, auth=(NEO4J_USER, NEO4J_PASSWORD))
        _driver.verify_connectivity()
    return _driver


def _rows(query: str, params: dict | None = None) -> list[dict[str, Any]]:
    d = _driver_get()
    with d.session(database=NEO4J_DATABASE, default_access_mode="READ") as s:
        result = s.run(query, params or {})
        out = [dict(r) for r in result]
        try:
            result.consume()
        except Exception:
            pass
    return out


def _fmt(rows: list[dict[str, Any]]) -> str:
    if not rows:
        return ""
    return "\n".join(json.dumps(r, default=str) for r in rows)


mcp = FastMCP("newvillacarmen-graph")


@mcp.tool()
def graph_schema() -> str:
    """Node labels + relationship types with counts. Call before authoring Cypher."""
    labels = _rows("MATCH (n) UNWIND labels(n) AS l RETURN l AS label, count(*) AS n ORDER BY n DESC")
    rels = _rows("MATCH ()-[r]->() RETURN type(r) AS t, count(*) AS n ORDER BY n DESC")
    lines = ["NODE LABELS"]
    lines += [f"  {r['label']}: {r['n']}" for r in labels]
    lines.append("RELATIONSHIP TYPES")
    lines += [f"  {r['t']}: {r['n']}" for r in rels]
    return "\n".join(lines)


@mcp.tool()
def graph_query(cypher: str, limit: int = 50) -> str:
    """Run read-only Cypher against the code graph. Adds a LIMIT when missing."""
    if re.search(r"\b(CREATE|MERGE|DELETE|DETACH|SET|REMOVE|DROP|LOAD CSV)\b", cypher, re.I):
        return "Rejected: write query"
    if not re.search(r"\bLIMIT\b", cypher, re.I):
        cypher = cypher.rstrip().rstrip(";") + f" LIMIT {limit}"
    return _fmt(_rows(cypher))


@mcp.tool()
def graph_impact(target: str, kind: str = "auto") -> str:
    """Blast radius: what a change to a table/function/endpoint/component/file affects.
    kind auto-detects; force one of table|func|endpoint|component|file."""
    t = target.strip()
    tl = t.lower()
    detected = (
        kind
        if kind != "auto"
        else ("endpoint" if _is_endpoint(t) else "file" if t.endswith((".go", ".ts", ".tsx")) else "component" if _is_component(t) else None)
    )
    sections: list[str] = []

    def add(title: str, rows: list[dict[str, Any]]) -> None:
        if rows:
            sections.append(f"== {title} ==\n" + _fmt(rows))

    if detected in ("table", None):
        add("ENDPOINTS REACHING THIS TABLE (<=3 call hops)", _rows(
            "MATCH (tb:Table) WHERE toLower(tb.name) = $tl "
            "MATCH (tb)<-[acc:READS|WRITES]-(sink:Func)<-[:CALLS*0..3]-(h:Func)<-[:HANDLED_BY]-(e:Endpoint) "
            "RETURN DISTINCT e.method + ' ' + e.path AS endpoint, type(acc) AS access, "
            "sink.name AS accessor ORDER BY endpoint LIMIT 60",
            {"tl": tl}))
        add("FRONTEND CODE AFFECTED (via the endpoints above)", _rows(
            "MATCH (tb:Table) WHERE toLower(tb.name) = $tl "
            "MATCH (tb)<-[:READS|WRITES]-(:Func)<-[:HANDLED_BY]-(be:Endpoint) "
            "OPTIONAL MATCH (fe:Endpoint)-[:SERVED_BY]->(be) "
            "WITH be, coalesce(fe, be) AS entry "
            "MATCH (src)-[:FETCHES]->(entry) WHERE src:Component OR src:File "
            "RETURN DISTINCT coalesce(src.repo, 'n/a') AS repo, "
            "labels(src)[0] AS kind, coalesce(src.name, src.path) AS caller, "
            "count(DISTINCT be) AS endpoints ORDER BY endpoints DESC, caller LIMIT 40",
            {"tl": tl}))

    if detected in ("func", None):
        add("CALLERS (who depends on this function)", _rows(
            "MATCH (f:Func) WHERE toLower(f.name) = $tl OR f.key = $t "
            "MATCH (c:Func)-[:CALLS]->(f) "
            "RETURN DISTINCT c.key AS caller, c.file AS file ORDER BY caller LIMIT 40",
            {"tl": tl, "t": t}))
        add("WHAT THIS FUNCTION TOUCHES", _rows(
            "MATCH (f:Func) WHERE toLower(f.name) = $tl OR f.key = $t "
            "OPTIONAL MATCH (f)-[a:READS|WRITES]->(tb:Table) "
            "OPTIONAL MATCH (f)-[:USES_ENV]->(v:EnvVar) "
            "RETURN DISTINCT f.key AS fn, type(a) AS access, tb.name AS `table`, "
            "v.name AS env LIMIT 40",
            {"tl": tl, "t": t}))

    if detected in ("endpoint", None):
        add("ENDPOINT -> HANDLER -> DATA", _rows(
            "MATCH (e:Endpoint) WHERE toLower(e.path) = $tl OR toLower(e.key) = $tl "
            "OR toLower(e.path) CONTAINS $tl "
            "MATCH (e)-[:HANDLED_BY]->(f:Func) "
            "OPTIONAL MATCH (f)-[:CALLS*0..2]->(:Func)-[:READS|WRITES]->(tb:Table) "
            "RETURN DISTINCT e.key AS endpoint, f.key AS handler, f.file AS file, "
            "collect(DISTINCT tb.name)[0..10] AS tables LIMIT 30",
            {"tl": tl}))

    if detected in ("component", None):
        add("WHAT THIS COMPONENT DEPENDS ON (endpoints -> tables)", _rows(
            "MATCH (c:Component) WHERE toLower(c.name) = $tl OR c.key = $t "
            "MATCH (c)-[:FETCHES]->(entry:Endpoint) "
            "OPTIONAL MATCH (entry)-[:SERVED_BY]->(be:Endpoint) "
            "WITH c, coalesce(be, entry) AS ep "
            "OPTIONAL MATCH (ep)-[:HANDLED_BY]->(:Func)-[:CALLS*0..2]->(:Func)-[:READS|WRITES]->(tb:Table) "
            "RETURN DISTINCT c.name AS component, ep.method + ' ' + ep.path AS endpoint, "
            "collect(DISTINCT tb.name)[0..8] AS tables LIMIT 40",
            {"tl": tl, "t": t}))
        add("WHO RENDERS THIS COMPONENT", _rows(
            "MATCH (c:Component) WHERE toLower(c.name) = $tl OR c.key = $t "
            "MATCH (parent:Component)-[:RENDERS]->(c) "
            "RETURN DISTINCT parent.name AS renderedBy, parent.file AS file LIMIT 30",
            {"tl": tl, "t": t}))

    if detected == "file":
        add("WHAT THIS FILE DECLARES, AND WHO USES IT", _rows(
            "MATCH (fl:File) WHERE fl.path CONTAINS $t "
            "OPTIONAL MATCH (fl)-[:DECLARES]->(f:Func)<-[:CALLS]-(c:Func) "
            "WHERE NOT c.file = fl.path "
            "RETURN DISTINCT fl.path AS file, f.name AS declares, "
            "count(DISTINCT c) AS externalCallers ORDER BY externalCallers DESC LIMIT 40",
            {"t": t}))

    if not sections:
        return f'Nothing in the graph matches "{t}". New code, or stale index — run /graph reindex.'
    return "\n\n".join(sections)


@mcp.tool()
def graph_orphan_calls(repo: str = "all") -> str:
    """Frontend API calls with no matching backend route (404 at runtime). repo=all|preact|backoffice."""
    rows = _rows(
        "MATCH (fe:Endpoint) WHERE fe.calledFrom IS NOT NULL "
        "AND NOT exists((fe)-[:HANDLED_BY]->()) AND NOT exists((fe)-[:SERVED_BY]->()) "
        "MATCH (src)-[r:FETCHES]->(fe) "
        "WHERE $repo = 'all' OR coalesce(src.repo, '') = $repo "
        "RETURN fe.key AS unservedCall, coalesce(src.name, src.path) AS calledFrom, "
        "r.line AS line ORDER BY unservedCall LIMIT 80",
        {"repo": repo})
    if not rows:
        return "Every frontend API call resolves to a backend route."
    return f"{_fmt(rows)}\n\n{len(rows)} frontend call site(s) have no matching backend route."


def _is_endpoint(t: str) -> bool:
    return bool(re.match(r"^(GET|POST|PUT|DELETE|PATCH|ANY)\s", t)) or t.startswith("/")


def _is_component(t: str) -> bool:
    return bool(re.match(r"^[A-Z][A-Za-z0-9_]*$", t))


if __name__ == "__main__":
    mcp.run()
