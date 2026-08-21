"""DSPy signatures: the article's 5 prompts, as typed declarations.

A signature says *what* the transformation is, not how to phrase it. The
wording below is a starting instruction; `optimize.py` rewrites it against a
metric so the prompt improves from data instead of guesswork.
"""

from __future__ import annotations

import dspy


class TranslateToCypher(dspy.Signature):
    """Translate a question about the codebase into ONE read-only Cypher query.

    Rules:
    - Use ONLY the labels, relationship types and properties listed in `graph_schema`.
      Never invent a label, relationship or property.
    - Read-only: MATCH / OPTIONAL MATCH / WHERE / WITH / RETURN / ORDER BY /
      LIMIT only. Never CREATE, MERGE, SET, DELETE, REMOVE or CALL.
    - Traversals must be bounded: at most 3 hops (e.g. `*1..3`).
    - Always end with a LIMIT.
    - RETURN scalar properties, never whole nodes. Use `RETURN e.key AS endpoint`,
      not `RETURN e`. Alias every returned column with AS.
    - Prefer exact property matches. For fuzzy names use
      `toLower(n.name) CONTAINS toLower($term)` and pass the term via `params`.
    - Every `$parameter` used in the query MUST have a value in `params`.
    """

    graph_schema: str = dspy.InputField(desc="the literal graph schema; the only allowed vocabulary")
    question: str = dspy.InputField(desc="a question about the codebase")
    reasoning_hint: str = dspy.InputField(desc="optional hint about which nodes matter", default="")
    cypher: str = dspy.OutputField(desc="one read-only Cypher query")
    params: dict[str, str] = dspy.OutputField(desc="parameter values referenced by the query")


class RepairCypher(dspy.Signature):
    """Fix a Cypher query that failed to parse or returned nothing.

    Keep the original intent. Change only what is necessary, and stay inside the
    vocabulary given by `schema`.
    """

    graph_schema: str = dspy.InputField()
    question: str = dspy.InputField()
    cypher: str = dspy.InputField(desc="the query that failed")
    error: str = dspy.InputField(desc="database error, or 'empty result'")
    fixed_cypher: str = dspy.OutputField()
    params: dict[str, str] = dspy.OutputField()


class GroundedAnswer(dspy.Signature):
    """Answer a question about the codebase using ONLY the retrieved subgraph.

    Rules:
    - The rows in `subgraph` are the result of running `cypher`. The query's
      MATCH pattern is itself evidence: rows returned by a query that filters on
      `t.name = 'bookings'` are, by construction, about `bookings`. Do not claim
      a relationship is missing when the query already traversed it.
    - Every claim must be traceable to a row in `subgraph`. Cite the node key or
      relationship that supports it.
    - Do not infer causation from co-occurrence. `CALLS` and `WRITES` are
      claims about behaviour; `IMPORTS` and `IN` are only structural proximity.
    - If the subgraph does not contain the answer, set `sufficient=false` and
      say what is missing. Never fill gaps from memory of similar codebases.
    - Prefer concrete identifiers (file paths, function keys, endpoint paths)
      over prose.
    """

    question: str = dspy.InputField()
    cypher: str = dspy.InputField(desc="the query that produced the rows; it defines what they mean")
    subgraph: str = dspy.InputField(desc="rows returned by the Cypher query")
    answer: str = dspy.OutputField(desc="grounded answer with concrete identifiers")
    citations: list[str] = dspy.OutputField(desc="node keys / paths that support the answer")
    sufficient: bool = dspy.OutputField(desc="false if the subgraph lacks the answer")


class ExtractCodeFacts(dspy.Signature):
    """Extract relations from prose about the codebase (docs, commits, reviews).

    Rules:
    - `evidence` MUST be an exact substring of `text`. No quote means no
      relation: drop it instead of guessing.
    - `subject` and `object` should match a symbol in `known_symbols` when the
      prose refers to one.
    - Use only these relation types: DOCUMENTED_IN, EXPLAINS, CONSTRAINS,
      SUPERSEDES, CAUSED_BY.
    """

    text: str = dspy.InputField(desc="prose to extract from")
    known_symbols: str = dspy.InputField(desc="symbols that exist in the graph")
    relations: list[dict] = dspy.OutputField(
        desc="[{subject, type, object, evidence, confidence}] with exact-quote evidence"
    )


class ResolveEntity(dspy.Signature):
    """Decide whether a candidate name refers to an existing graph node.

    Run before insertion. Merging duplicates after the fact is far harder than
    not creating them.
    """

    candidate: str = dspy.InputField(desc="name found in prose")
    existing: str = dspy.InputField(desc="candidate nodes already in the graph")
    match_key: str = dspy.OutputField(desc="exact key of the matching node, or empty if new")
    is_new: bool = dspy.OutputField()
