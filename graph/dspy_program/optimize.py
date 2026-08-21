"""Evaluate and optimize the GraphRAG program.

    python dspy_program/optimize.py --baseline          # measure as-is
    python dspy_program/optimize.py --compile           # optimize + save
    python dspy_program/optimize.py --ask "question"    # single query

The optimizer needs no hand-labelled data: metrics.build_trainset derives
ground truth from the deterministic graph.
"""

from __future__ import annotations

import argparse
import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

import dspy  # noqa: E402

from dspy_program.common import GRAPH_DIR, Graph, configure_lm  # noqa: E402
from dspy_program.metrics import answer_metric, build_trainset, retrieval_metric  # noqa: E402
from dspy_program.modules import GraphRAG  # noqa: E402

COMPILED = GRAPH_DIR / "dspy_program" / "compiled" / "graphrag.json"


def split(examples, frac=0.5):
    cut = int(len(examples) * frac)
    return examples[:cut], examples[cut:]


def evaluate(program, devset, metric, threads: int = 4, label: str = "") -> float:
    ev = dspy.Evaluate(
        devset=devset,
        metric=metric,
        num_threads=threads,
        display_progress=True,
        display_table=0,
        provide_traceback=True,
    )
    result = ev(program)
    score = result.score if hasattr(result, "score") else float(result)
    print(f"\n{label} score: {score:.4f} over {len(devset)} examples")
    return score


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--baseline", action="store_true")
    ap.add_argument("--compile", action="store_true")
    ap.add_argument("--ask")
    ap.add_argument("--n", type=int, default=8, help="examples per question kind")
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--auto", default="light", choices=["light", "medium", "heavy"])
    args = ap.parse_args()

    configure_lm()
    graph = Graph()
    program = GraphRAG(graph)

    if COMPILED.exists() and not args.compile:
        program.load(str(COMPILED))
        print(f"loaded optimized prompts from {COMPILED.name}", file=sys.stderr)

    if args.ask:
        pred = program(question=args.ask)
        print("\nCYPHER:\n" + pred.cypher)
        print("\nROWS:", len(pred.rows))
        print("\nANSWER:\n" + pred.answer)
        if pred.citations:
            print("\nCITATIONS:", pred.citations)
        print("\nSUFFICIENT:", pred.sufficient)
        return 0

    examples = build_trainset(graph, n_per_kind=args.n)
    trainset, devset = split(examples)
    print(f"trainset={len(trainset)} devset={len(devset)}", file=sys.stderr)

    if args.baseline:
        evaluate(program, devset, retrieval_metric, args.threads, "baseline retrieval")
        return 0

    if args.compile:
        before = evaluate(program, devset, retrieval_metric, args.threads, "before")
        opt = dspy.MIPROv2(metric=retrieval_metric, auto=args.auto, num_threads=args.threads)
        compiled = opt.compile(program, trainset=trainset, requires_permission_to_run=False)
        after = evaluate(compiled, devset, retrieval_metric, args.threads, "after")

        COMPILED.parent.mkdir(parents=True, exist_ok=True)
        compiled.save(str(COMPILED))
        print(json.dumps({"before": before, "after": after, "saved": str(COMPILED)}, indent=2))
        return 0

    ap.print_help()
    return 1


if __name__ == "__main__":
    sys.exit(main())
