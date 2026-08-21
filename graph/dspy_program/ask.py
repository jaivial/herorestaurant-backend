"""One-shot CLI bridge: question in, JSON out.

The pi extension shells out to this so the TypeScript side never needs a Python
runtime or the DSPy dependency tree.

    .venv/bin/python dspy_program/ask.py "which endpoints write to bookings?"
"""

from __future__ import annotations

import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from dspy_program.common import GRAPH_DIR, Graph, configure_lm  # noqa: E402
from dspy_program.modules import GraphRAG  # noqa: E402

COMPILED = GRAPH_DIR / "dspy_program" / "compiled" / "graphrag.json"


def main() -> int:
    if len(sys.argv) < 2:
        print(json.dumps({"error": "usage: ask.py <question>"}))
        return 1
    question = " ".join(sys.argv[1:])

    try:
        configure_lm()
        graph = Graph()
        program = GraphRAG(graph)
        if COMPILED.exists():
            program.load(str(COMPILED))

        pred = program(question=question)
        print(
            json.dumps(
                {
                    "answer": pred.answer,
                    "cypher": pred.cypher,
                    "citations": list(pred.citations)[:40],
                    "sufficient": bool(pred.sufficient),
                    "rowCount": len(pred.rows),
                    "rows": pred.rows[:25],
                },
                default=str,
            )
        )
        return 0
    except Exception as exc:  # noqa: BLE001 - reported as JSON to the caller
        print(json.dumps({"error": f"{type(exc).__name__}: {exc}"}))
        return 1


if __name__ == "__main__":
    sys.exit(main())
