# Code graph (graph engineering)

Grep finds text. This finds *connections*.

> "What breaks if I change the `bookings` table?"

is a path across `Table <- Func <- CALLS* <- Func <- Endpoint`. Text search
cannot answer it; a graph query can. On this codebase the difference is
concrete: **40 endpoints touch `bookings` directly, 54 once you follow the call
graph.** The other 14 are exactly the ones a grep-driven change would break.

## Layers

| Layer | What it does | Where |
|---|---|---|
| 1 Ingestion | the Go backend as it is on disk | — |
| 2 Extraction | `go/packages` + `go/ast`, **no LLM** | `extractors/gograph/` |
| 3 Resolution | stable keys (`pkg.(*Recv).Method`) make duplicates impossible | in the extractor |
| 4 Storage | Neo4j 5, constraints + fulltext index | `schema.cypher` |
| 5 Retrieval | read-only Cypher, bounded traversal | `dspy_program/common.py` |
| 6 Agent | pi tools + the DSPy program | `.pi/extensions/graph/` |
| 7 Verification | citations re-checked against returned rows | `dspy_program/metrics.py` |
| 8 Update | `MERGE`-only, so re-indexing is idempotent | `loader/load.py` |

**Extraction is deterministic on purpose.** The compiler already knows what
calls what; asking a model to guess would be slower, costlier and wrong. The
LLM is used only where facts are genuinely fuzzy (natural-language questions).

## Setup

```bash
cd graph
cp .env.example .env         # set NEO4J_PASSWORD
make up                      # start Neo4j (bolt on 127.0.0.1 only)
make schema                  # constraints + indexes
make setup                   # python env for the DSPy layer
make index                   # extract + load  (~6s for 347 Go files)
make status
```

`make index` is safe to re-run: every write is a `MERGE` on a unique key, so
re-indexing updates facts in place rather than duplicating them.

## What is in the graph

4.4k nodes / 23k edges from the backend:

```
Func 2749   File 347   Endpoint 675   Table 187   Package 233   EnvVar 42
CALLS 18k   DECLARES 3.7k   IMPORTS 1.4k   HANDLED_BY 675   READS/WRITES 900
```

All 675 endpoints resolve to a handler, including the
`handleListSites(db)` factory pattern that returns an `http.Handler`.

## Asking questions

```bash
make ask Q="which endpoints write to the bookings table?"
```

Or from pi, via four tools:

| Tool | Use it for |
|---|---|
| `graph_schema` | the real labels/properties, before writing Cypher |
| `graph_query` | precise structural questions (read-only, ≤3 hops, auto-`LIMIT`) |
| `graph_impact` | **blast radius** of changing a table/func/endpoint/file |
| `graph_ask` | natural language, answered with citations |

`/graph status` and `/graph reindex` manage it. After editing a `.go` file, a
`tool_result` hook appends the file's blast radius automatically.

### Queries worth knowing

```cypher
-- everything that reaches a table, transitively
MATCH (tb:Table {name:'bookings'})<-[:READS|WRITES]-(:Func)<-[:CALLS*0..3]-(h:Func)
      <-[:HANDLED_BY]-(e:Endpoint)
RETURN DISTINCT e.method + ' ' + e.path AS endpoint ORDER BY endpoint;

-- which config an endpoint depends on
MATCH (e:Endpoint {key:'POST /admin/login'})-[:HANDLED_BY]->(f:Func)-[:CALLS*0..2]->(g:Func)
MATCH (g)-[:USES_ENV]->(v:EnvVar) RETURN DISTINCT v.name;

-- dead code candidates: exported, never called
MATCH (f:Func) WHERE f.exported AND f.file IS NOT NULL AND NOT (f)<-[:CALLS]-()
  AND NOT (f)<-[:HANDLED_BY]-() RETURN f.key, f.file LIMIT 40;
```

## The DSPy layer

The 5 prompts from the article are typed `dspy.Signature` declarations in
`dspy_program/signatures.py`, wired together in `modules.py` as
translate → validate → execute → answer → verify, with a repair step on failure.

The reason DSPy is worth it here: **optimizing prompts normally needs labelled
data, and a deterministic graph produces it for free.** `metrics.py` generates
questions whose answers it already knows (`build_trainset`), then scores the
model on retrieval recall, precision and citation grounding — no human labels.

```bash
make baseline    # measure current prompts
make optimize    # MIPROv2, saves dspy_program/compiled/graphrag.json
```

Measured on this repo: **85.9% → 100%** retrieval score. The optimizer
discovered the conventions on its own — that `Func` is keyed by `key` while
`Table` is keyed by `name`, and that `DISTINCT` is needed because multiple
call paths reach the same endpoint. Commit the compiled artifact: it is the
prompt, versioned and reproducible.

## Safety

`graph_query` and the DSPy layer share one validator:

- writes rejected (`CREATE`/`MERGE`/`SET`/`DELETE`/`DROP`/`LOAD CSV`)
- only read-only `db.*` procedures may be called
- variable-length patterns capped at 3 hops
- `LIMIT` injected when absent; 30s server-side transaction timeout
- single statement only

Values reach the database as real query parameters (`cypher-shell -P`), never
string-interpolated, and Neo4j binds to `127.0.0.1` because the rest of the
stack uses host networking.

## Layout

```
graph/
  docker-compose.graph.yml   schema.cypher   Makefile   requirements.txt
  extractors/gograph/        nested Go module: x/tools stays out of the
                             backend's vendor tree and `go build ./...`
  loader/load.py             idempotent MERGE loader
  dspy_program/
    common.py                LM config, Cypher validator, schema introspection
    signatures.py            the 5 prompts, as typed declarations
    modules.py               GraphRAG: translate -> validate -> answer -> verify
    metrics.py               self-labelling trainset + label-free metrics
    optimize.py ask.py       CLI entry points
    compiled/graphrag.json   optimized prompts (committed)
.pi/extensions/graph/index.ts
```

## Extending to the other repos

`preactvillacarmen` and `backoffice` need a TypeScript extractor emitting the
same `triples.jsonl` shape (`Component`, `Route`, `FETCHES`). Once
`(:Component)-[:FETCHES]->(:Endpoint)` exists, blast radius spans the full
stack: change a column, see which React components break.
