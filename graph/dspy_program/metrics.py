"""Metrics and a self-labelling trainset.

The usual blocker for DSPy is needing hand-labelled data. A deterministic code
graph removes it: questions can be generated together with their ground-truth
answers by querying the graph directly. The metric then compares the model's
retrieval against facts the compiler already verified.
"""

from __future__ import annotations

import random

import dspy

from .common import UnsafeCypher, validate_readonly


# ---------------------------------------------------------------- trainset gen

def build_trainset(graph, n_per_kind: int = 12, seed: int = 7) -> list[dspy.Example]:
    """Generate questions whose correct answers are already known.

    Each template pairs a natural-language question with a Cypher query the
    graph can answer exactly. The expected answer set becomes the label.
    """
    rng = random.Random(seed)
    examples: list[dspy.Example] = []

    def add(question: str, gold: list[str], hint: str = "") -> None:
        if gold:
            examples.append(
                dspy.Example(question=question, gold=sorted(set(gold)), reasoning_hint=hint)
                .with_inputs("question", "reasoning_hint")
            )

    # 1. Which endpoints touch a table (transitively).
    tables = [
        r["name"]
        for r in graph.run(
            "MATCH (t:Table)<-[:READS|WRITES]-(:Func) "
            "WITH t, count(*) AS c WHERE c > 1 "
            "RETURN t.name AS name ORDER BY c DESC LIMIT 40"
        )
    ]
    for t in rng.sample(tables, min(n_per_kind, len(tables))):
        gold = [
            r["k"]
            for r in graph.run(
                "MATCH (t:Table {name:$t})<-[:READS|WRITES]-(f:Func)<-[:HANDLED_BY]-(e:Endpoint) "
                "RETURN DISTINCT e.key AS k LIMIT 60",
                {"t": t},
            )
        ]
        add(f"Which HTTP endpoints read or write the `{t}` table?", gold)

    # 2. Which table does an endpoint's handler touch.
    eps = [
        r["k"]
        for r in graph.run(
            "MATCH (e:Endpoint)-[:HANDLED_BY]->(f:Func)-[:READS|WRITES]->(:Table) "
            "RETURN DISTINCT e.key AS k LIMIT 60"
        )
    ]
    for e in rng.sample(eps, min(n_per_kind, len(eps))):
        gold = [
            r["n"]
            for r in graph.run(
                "MATCH (e:Endpoint {key:$e})-[:HANDLED_BY]->(f:Func)-[:READS|WRITES]->(t:Table) "
                "RETURN DISTINCT t.name AS n",
                {"e": e},
            )
        ]
        add(f"Which database tables does the endpoint `{e}` touch?", gold)

    # 3. Which function handles an endpoint.
    for e in rng.sample(eps, min(n_per_kind, len(eps))):
        gold = [
            r["k"]
            for r in graph.run(
                "MATCH (e:Endpoint {key:$e})-[:HANDLED_BY]->(f:Func) RETURN f.key AS k",
                {"e": e},
            )
        ]
        add(f"Which Go function handles `{e}`?", gold)

    # 4. Who calls a function (fan-in).
    funcs = [
        r["k"]
        for r in graph.run(
            "MATCH (f:Func)<-[:CALLS]-() WITH f, count(*) AS c "
            "WHERE c >= 2 AND c <= 8 AND f.file IS NOT NULL "
            "RETURN f.key AS k ORDER BY c DESC LIMIT 60"
        )
    ]
    for f in rng.sample(funcs, min(n_per_kind, len(funcs))):
        short = f.rsplit(".", 1)[-1]
        gold = [
            r["k"]
            for r in graph.run(
                "MATCH (c:Func)-[:CALLS]->(f:Func {key:$f}) RETURN DISTINCT c.key AS k LIMIT 40",
                {"f": f},
            )
        ]
        add(f"Which functions call `{short}` (full key `{f}`)?", gold)

    # 5. Which environment variables a file's functions read.
    envs = [
        r["n"]
        for r in graph.run(
            "MATCH (:Func)-[:USES_ENV]->(v:EnvVar) RETURN DISTINCT v.name AS n LIMIT 40"
        )
    ]
    for v in rng.sample(envs, min(n_per_kind, len(envs))):
        gold = [
            r["k"]
            for r in graph.run(
                "MATCH (f:Func)-[:USES_ENV]->(:EnvVar {name:$v}) RETURN DISTINCT f.key AS k",
                {"v": v},
            )
        ]
        add(f"Which functions read the environment variable `{v}`?", gold)

    rng.shuffle(examples)
    return examples


# --------------------------------------------------------------------- metrics

def _flatten(rows: list[dict]) -> set[str]:
    """Every scalar string in the result, for order-insensitive comparison."""
    out: set[str] = set()
    for r in rows:
        for v in r.values():
            if isinstance(v, str):
                out.add(v)
            elif isinstance(v, list):
                out.update(x for x in v if isinstance(x, str))
    return out


def retrieval_metric(example, pred, trace=None) -> float:
    """Score retrieval against graph-derived ground truth.

    Components, each independently checkable without a human:
      safety   the query is read-only and bounded
      parses   the database accepted it
      recall   fraction of expected identifiers actually retrieved
      precise  penalty for dragging in unrelated rows
    """
    cypher = getattr(pred, "cypher", "") or ""
    rows = getattr(pred, "rows", []) or []
    gold = set(example.gold)

    try:
        validate_readonly(cypher)
        safety = 1.0
    except UnsafeCypher:
        return 0.0

    if not rows:
        return 0.05 * safety

    found = _flatten(rows)
    hit = len(gold & found)
    recall = hit / max(1, len(gold))
    # Precision proxy: how much of what came back was asked for. Guards against
    # a query that returns half the graph and technically contains the answer.
    precision = hit / max(1, len(found))

    score = 0.15 + 0.6 * recall + 0.25 * precision
    return round(min(1.0, score), 4)


def answer_metric(example, pred, trace=None) -> float:
    """Retrieval score, plus a check that the answer is actually grounded."""
    base = retrieval_metric(example, pred, trace)
    if base < 0.2:
        return base

    answer = (getattr(pred, "answer", "") or "").lower()
    citations = getattr(pred, "citations", []) or []
    rows_text = " ".join(sorted(_flatten(getattr(pred, "rows", []) or []))).lower()

    # Every citation must appear in what the database actually returned:
    # this is the verification layer, applied as a reward.
    if citations:
        grounded = sum(1 for c in citations if str(c).lower() in rows_text)
        grounded_frac = grounded / len(citations)
    else:
        grounded_frac = 0.0

    mentions = sum(1 for g in example.gold if str(g).lower() in answer)
    mention_frac = mentions / max(1, len(example.gold))

    return round(0.5 * base + 0.3 * grounded_frac + 0.2 * mention_frac, 4)
