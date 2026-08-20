"""Shared setup for the DSPy layer: LM wiring and the Neo4j accessor.

Two things live here because both the runtime program and the optimizer need
them identically: the language model configuration and a read-only graph client
whose safety rules are enforced in one place.
"""

from __future__ import annotations

import os
import pathlib
import re

from dotenv import load_dotenv

GRAPH_DIR = pathlib.Path(__file__).resolve().parent.parent
load_dotenv(GRAPH_DIR / ".env")


def _patch_litellm() -> None:
    """Work around a litellm/pydantic forward-reference bug.

    litellm 1.97 annotates Message with a TypedDict it never imports into the
    module namespace, so pydantic cannot finish building the model and every
    completion raises PydanticUserError. Importing the symbol and rebuilding
    fixes it without touching site-packages.
    """
    try:
        import litellm.types.utils as lu

        if getattr(lu, "ChatCompletionReasoningSummaryTextBlock", None) is None:
            raise AttributeError
    except AttributeError:
        import litellm.types.utils as lu
        from litellm.types.llms.openai import ChatCompletionReasoningSummaryTextBlock

        lu.ChatCompletionReasoningSummaryTextBlock = ChatCompletionReasoningSummaryTextBlock
        lu.Message.model_rebuild()


def configure_lm(max_tokens: int = 16000, temperature: float = 1.0):
    """Point DSPy at the cliproxy gateway."""
    _patch_litellm()
    import dspy

    lm = dspy.LM(
        os.environ.get("GRAPH_LM_MODEL", "openai/opencode-go-acct1/hy3"),
        api_base=os.environ.get("GRAPH_LM_BASE_URL", "http://127.0.0.1:8317/v1"),
        api_key=os.environ.get("GRAPH_LM_API_KEY", ""),
        # hy3 is a reasoning model: it spends tokens thinking before it answers,
        # so a small cap yields an empty completion rather than a short one.
        max_tokens=max_tokens,
        temperature=temperature,
    )
    dspy.configure(lm=lm)
    return lm


# --------------------------------------------------------------- graph client

WRITE_CLAUSES = re.compile(
    r"\b(create|merge|delete|detach|set|remove|drop|foreach|load\s+csv)\b", re.I
)
# Procedures that can mutate or exfiltrate; the read-only allowlist is narrow
# on purpose.
CALL_ALLOWED = re.compile(r"\bcall\s+(db\.labels|db\.relationshipTypes|db\.propertyKeys|db\.index\.fulltext\.queryNodes)\b", re.I)
CALL_ANY = re.compile(r"\bcall\b", re.I)
VAR_LEN = re.compile(r"\*\s*(\d*)\s*\.\.\s*(\d*)")


class UnsafeCypher(ValueError):
    pass


def validate_readonly(cypher: str, max_hops: int = 3) -> str:
    """Reject anything that could write, escape or run away.

    Layered with a read-only DB user and a server-side transaction timeout:
    this is the first gate, not the only one.
    """
    q = cypher.strip().rstrip(";")
    if not q:
        raise UnsafeCypher("empty query")
    if ";" in q:
        raise UnsafeCypher("multiple statements are not allowed")
    if WRITE_CLAUSES.search(q):
        raise UnsafeCypher("write clause detected; only read queries are allowed")
    if CALL_ANY.search(q) and not CALL_ALLOWED.search(q):
        raise UnsafeCypher("only read-only db.* procedures may be called")
    for lo, hi in VAR_LEN.findall(q):
        upper = int(hi) if hi else 99
        if upper > max_hops:
            raise UnsafeCypher(f"variable-length pattern exceeds {max_hops} hops")
    if not re.search(r"\blimit\s+\d+", q, re.I):
        q += f"\nLIMIT 50"
    return q


class Graph:
    """Thin read-only Neo4j accessor used by every DSPy module."""

    def __init__(self) -> None:
        from neo4j import GraphDatabase

        self._driver = GraphDatabase.driver(
            os.environ.get("NEO4J_URI", "bolt://127.0.0.1:7687"),
            auth=(
                os.environ.get("NEO4J_USER", "neo4j"),
                os.environ["NEO4J_PASSWORD"],
            ),
        )
        self._schema_cache: str | None = None

    def close(self) -> None:
        self._driver.close()

    def run(self, cypher: str, params: dict | None = None, timeout: float = 20.0):
        with self._driver.session(default_access_mode="READ") as s:
            return [r.data() for r in s.run(cypher, params or {}, timeout=timeout)]

    def explain_ok(self, cypher: str, params: dict | None = None) -> tuple[bool, str]:
        """Parse+plan without executing. Catches syntax and unknown properties."""
        try:
            with self._driver.session(default_access_mode="READ") as s:
                s.run("EXPLAIN " + cypher.rstrip(";"), params or {}).consume()
            return True, ""
        except Exception as exc:  # noqa: BLE001 - surfaced back to the model
            return False, str(exc).split("\n")[0]

    def schema_literal(self) -> str:
        """The exact labels, relationship types and properties in the database.

        Passed verbatim to the model: guessing schema is the single largest
        source of broken generated Cypher.
        """
        if self._schema_cache:
            return self._schema_cache
        labels = [r["label"] for r in self.run("CALL db.labels() YIELD label RETURN label ORDER BY label")]
        rels = [
            r["relationshipType"]
            for r in self.run(
                "CALL db.relationshipTypes() YIELD relationshipType RETURN relationshipType ORDER BY relationshipType"
            )
        ]
        lines = ["NODE LABELS AND PROPERTIES:"]
        for lab in labels:
            cnt = self.run(f"MATCH (n:`{lab}`) RETURN count(n) AS c")[0]["c"]
            if cnt == 0:
                # Declared by a constraint but unpopulated: offering it to the
                # model only invites queries that return nothing.
                continue
            props = self.run(
                f"MATCH (n:`{lab}`) WITH n LIMIT 200 "
                "UNWIND keys(n) AS k RETURN DISTINCT k ORDER BY k"
            )
            keys = ", ".join(p["k"] for p in props)
            lines.append(f"  (:{lab} {{{keys}}})  -- {cnt} nodes")

        lines.append("\nRELATIONSHIP TYPES (observed patterns):")
        pattern = self.run(
            "MATCH (a)-[r]->(b) "
            "RETURN DISTINCT labels(a)[0] AS from, type(r) AS rel, labels(b)[0] AS to, count(*) AS n "
            "ORDER BY rel, from"
        )
        for p in pattern:
            lines.append(f"  (:{p['from']})-[:{p['rel']}]->(:{p['to']})  -- {p['n']}")
        lines.append("\nAVAILABLE RELATIONSHIP TYPES: " + ", ".join(rels))
        self._schema_cache = "\n".join(lines)
        return self._schema_cache
