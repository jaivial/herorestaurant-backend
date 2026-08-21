"""The GraphRAG module: layer 6 (agent) and layer 7 (verification).

Flow: schema -> Cypher -> validate -> execute -> answer -> verify citations.
Failures feed back into a repair step rather than being surfaced as an error,
which is what turns a brittle text-to-Cypher call into something usable.
"""

from __future__ import annotations

import json
import re

import dspy

from .common import Graph, UnsafeCypher, validate_readonly
from .signatures import GroundedAnswer, RepairCypher, TranslateToCypher

MAX_ROWS = 60
PARAM_RE = re.compile(r"\$([A-Za-z_][A-Za-z0-9_]*)")
WORD_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
STOPWORDS = {
    "which", "what", "where", "does", "handlers", "handler", "endpoint",
    "endpoints", "component", "components", "function", "functions", "table",
    "tables", "file", "files", "route", "routes", "read", "reads", "write",
    "writes", "call", "calls", "fetch", "fetches", "from", "that", "this",
    "with", "into", "when", "change", "changes", "breaks", "break", "using",
    "uses", "have", "has", "are", "the", "and", "for",
}


def resolve_anchors(graph, question: str, limit: int = 8) -> str:
    """Name the graph nodes a question mentions, before the model guesses.

    Wording and storage rarely agree: "bookings endpoints" is a `bookings`
    :Table, while the routes spell it `/public/booking/...`. Left alone the
    model substring-matches on path, gets one row instead of three, and the
    result is valid Cypher with a wrong answer -- the one failure the repair
    loop cannot see, because a short result is not an error.
    """
    words = {w.lower() for w in WORD_RE.findall(question) if len(w) > 3}
    terms = words - STOPWORDS
    if not terms:
        return ""
    # Both spellings: questions say "bookings", the table may be either.
    terms |= {t[:-1] for t in terms if t.endswith("s")}
    rows = graph.run(
        "UNWIND $terms AS term "
        "MATCH (n) WHERE (n:Table OR n:Component OR n:Func OR n:EnvVar) "
        "AND toLower(coalesce(n.name, n.key)) = term "
        "RETURN DISTINCT labels(n)[0] AS label, coalesce(n.name, n.key) AS name "
        "ORDER BY label, name LIMIT $limit",
        {"terms": sorted(terms), "limit": limit},
    )
    if not rows:
        return ""
    found = ", ".join(f"(:{r['label']} {{name/key: '{r['name']}'}})" for r in rows)
    return (
        f"These nodes exist in the graph and match the question: {found}. "
        "Anchor the pattern on them and traverse relationships from there; "
        "do not substring-match a property instead."
    )


def missing_params(cypher: str, params: dict) -> list[str]:
    """Parameters referenced by the query but absent from the params map.

    Neo4j reports these only as a plan warning under EXPLAIN, so they would
    otherwise surface as a confusing runtime failure.
    """
    return sorted({m for m in PARAM_RE.findall(cypher) if m not in params})


def render_rows(rows: list[dict]) -> str:
    """Flatten result rows into compact text for the answer step."""
    if not rows:
        return "(no rows)"
    out = []
    for i, r in enumerate(rows[:MAX_ROWS]):
        parts = []
        for k, v in r.items():
            if isinstance(v, (dict, list)):
                v = json.dumps(v, ensure_ascii=False, default=str)
            parts.append(f"{k}={v}")
        out.append(f"[{i}] " + "  ".join(parts))
    if len(rows) > MAX_ROWS:
        out.append(f"... {len(rows) - MAX_ROWS} more rows omitted")
    return "\n".join(out)


class GraphRAG(dspy.Module):
    """Answer questions about the codebase over the Neo4j code graph."""

    def __init__(self, graph: Graph, max_attempts: int = 3):
        super().__init__()
        self.graph = graph
        self.max_attempts = max_attempts
        self.translate = dspy.ChainOfThought(TranslateToCypher)
        self.repair = dspy.ChainOfThought(RepairCypher)
        self.respond = dspy.ChainOfThought(GroundedAnswer)

    def forward(self, question: str, reasoning_hint: str = "") -> dspy.Prediction:
        schema = self.graph.schema_literal()
        attempts: list[dict] = []
        if not reasoning_hint:
            reasoning_hint = resolve_anchors(self.graph, question)

        pred = self.translate(graph_schema=schema, question=question, reasoning_hint=reasoning_hint)
        cypher, params = pred.cypher, pred.params or {}

        rows: list[dict] = []
        for attempt in range(self.max_attempts):
            error = ""
            safe = ""
            try:
                safe = validate_readonly(cypher)
                gaps = missing_params(safe, params)
                if gaps:
                    error = f"missing parameter values for: {', '.join('$' + g for g in gaps)}"
                else:
                    ok, err = self.graph.explain_ok(safe, params)
                    if not ok:
                        error = err
                    else:
                        rows = self.graph.run(safe, params)
                        if not rows:
                            error = "empty result"
            except UnsafeCypher as exc:
                error = f"unsafe query: {exc}"
            except Exception as exc:  # noqa: BLE001 - fed back to the repair step
                error = str(exc).split("\n")[0]

            attempts.append({"cypher": cypher, "error": error})
            if not error:
                break
            if attempt == self.max_attempts - 1:
                break
            fix = self.repair(
                graph_schema=schema, question=question, cypher=cypher, error=error
            )
            cypher, params = fix.fixed_cypher, fix.params or params

        if not rows:
            return dspy.Prediction(
                answer="The graph does not contain an answer to that question.",
                citations=[],
                sufficient=False,
                cypher=cypher,
                params=params,
                rows=[],
                attempts=attempts,
            )

        ans = self.respond(question=question, cypher=safe or cypher, subgraph=render_rows(rows))
        return dspy.Prediction(
            answer=ans.answer,
            citations=ans.citations or [],
            sufficient=bool(ans.sufficient),
            cypher=cypher,
            params=params,
            rows=rows,
            attempts=attempts,
        )
