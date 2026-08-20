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
