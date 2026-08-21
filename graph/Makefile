# Code graph: extract -> load -> query -> optimize.
#
#   make up        start Neo4j
#   make setup     create the Python env for the DSPy layer
#   make index     re-extract the Go code and reload the graph (idempotent)
#   make ask Q="which endpoints write to bookings?"
#   make optimize  tune the prompts against graph-derived ground truth

SHELL := /bin/bash
GRAPH_DIR := $(shell cd "$(dir $(lastword $(MAKEFILE_LIST)))" && pwd)
REPO_ROOT := $(abspath $(GRAPH_DIR)/..)
PY := $(GRAPH_DIR)/.venv/bin/python
COMPOSE := sudo docker compose --env-file $(GRAPH_DIR)/.env -f $(GRAPH_DIR)/docker-compose.graph.yml

.PHONY: up down logs setup schema extract extract-go extract-ts load index ask optimize baseline status clean

up:
	$(COMPOSE) up -d
	@echo "waiting for neo4j..."
	@for i in $$(seq 1 30); do \
		[ "$$(sudo docker inspect -f '{{.State.Health.Status}}' newvillacarmen-graph-neo4j 2>/dev/null)" = "healthy" ] && break; \
		sleep 3; \
	done
	@echo "neo4j ready on bolt://127.0.0.1:7687 (browser: http://127.0.0.1:7474)"

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail 100

setup:
	python3 -m venv $(GRAPH_DIR)/.venv
	$(PY) -m pip install -q --upgrade pip
	$(PY) -m pip install -q -r $(GRAPH_DIR)/requirements.txt
	@echo "python env ready"

schema:
	@set -a; . $(GRAPH_DIR)/.env; set +a; \
	sudo docker exec -i newvillacarmen-graph-neo4j cypher-shell -u neo4j -p "$$NEO4J_PASSWORD" \
		--format plain < $(GRAPH_DIR)/schema.cypher

# The three repos live side by side; the frontends are read from their working
# copies since only the backend is checked out in this worktree.
STACK_ROOT ?= /var/www/newvillacarmen
PREACT_ROOT ?= $(STACK_ROOT)/preactvillacarmen
BACKOFFICE_ROOT ?= $(STACK_ROOT)/backoffice
NODE ?= node

extract: extract-go extract-ts

extract-go:
	@mkdir -p $(GRAPH_DIR)/out
	cd $(GRAPH_DIR)/extractors/gograph && go run . \
		-root $(REPO_ROOT) -repo backend -out $(GRAPH_DIR)/out/triples.jsonl

extract-ts: $(GRAPH_DIR)/extractors/tsgraph/node_modules
	@mkdir -p $(GRAPH_DIR)/out
	cd $(GRAPH_DIR)/extractors/tsgraph && $(NODE) extract.mjs \
		--root $(PREACT_ROOT) --repo preact --out $(GRAPH_DIR)/out/triples.preact.jsonl
	cd $(GRAPH_DIR)/extractors/tsgraph && $(NODE) --max-old-space-size=6144 extract.mjs \
		--root $(BACKOFFICE_ROOT) --repo backoffice --out $(GRAPH_DIR)/out/triples.backoffice.jsonl

$(GRAPH_DIR)/extractors/tsgraph/node_modules:
	cd $(GRAPH_DIR)/extractors/tsgraph && npm install --no-audit --no-fund

load:
	$(PY) $(GRAPH_DIR)/loader/load.py --triples \
		$(GRAPH_DIR)/out/triples.jsonl \
		$(GRAPH_DIR)/out/triples.preact.jsonl \
		$(GRAPH_DIR)/out/triples.backoffice.jsonl

# Full refresh. MERGE semantics make this safe to run as often as you like.
index: extract load
	@echo "index complete"

status:
	@set -a; . $(GRAPH_DIR)/.env; set +a; \
	sudo docker exec newvillacarmen-graph-neo4j cypher-shell -u neo4j -p "$$NEO4J_PASSWORD" --format plain \
		"MATCH (n) WITH count(n) AS nodes MATCH ()-[r]->() RETURN nodes, count(r) AS rels"

ask:
	@$(PY) $(GRAPH_DIR)/dspy_program/optimize.py --ask "$(Q)"

baseline:
	$(PY) $(GRAPH_DIR)/dspy_program/optimize.py --baseline --n 4 --threads 6

optimize:
	$(PY) $(GRAPH_DIR)/dspy_program/optimize.py --compile --n 5 --threads 6 --auto light

clean:
	rm -rf $(GRAPH_DIR)/out
