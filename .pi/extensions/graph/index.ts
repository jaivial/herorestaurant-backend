/**
 * graph — code-graph tools for pi, backed by Neo4j.
 *
 * Why this exists: grep finds text, the graph finds connections. "What breaks
 * if I change the bookings table?" is a path query across
 * Table <- Func <- CALLS* <- Func <- Endpoint, which no amount of grepping
 * answers reliably.
 *
 * Tools:
 *   graph_schema   the literal labels/relationships in the database
 *   graph_query    run read-only Cypher (validated, bounded)
 *   graph_impact   blast radius for a table / function / endpoint / file
 *   graph_ask      natural-language question via the compiled DSPy program
 *
 * A tool_result hook appends blast radius after a Go file is edited, so the
 * consequences of a change are visible without having to ask.
 *
 * Commands: /graph status | reindex
 */

import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { Type } from "@sinclair/typebox";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

// The extension lives at <repo>/.pi/extensions/graph/index.ts
const REPO_ROOT = join(import.meta.dirname, "..", "..", "..");
const GRAPH_DIR = join(REPO_ROOT, "graph");
const VENV_PY = join(GRAPH_DIR, ".venv", "bin", "python");
const ASK_PY = join(GRAPH_DIR, "dspy_program", "ask.py");
const COMPILED = join(GRAPH_DIR, "dspy_program", "compiled", "graphrag.json");
const CONTAINER = "newvillacarmen-graph-neo4j";

const MAX_HOPS = 3;
const DEFAULT_LIMIT = 50;
const QUERY_TIMEOUT_MS = 30_000;
const ASK_TIMEOUT_MS = 300_000;

const text = (t: string) => ({ content: [{ type: "text" as const, text: t }], details: {} });

// ---------------------------------------------------------------------------
// Cypher safety
//
// Mirrors graph/dspy_program/common.py. Defence in depth alongside a
// server-side transaction timeout; not the only barrier.
// ---------------------------------------------------------------------------

const WRITE_CLAUSES = /\b(create|merge|delete|detach|set|remove|drop|foreach|load\s+csv)\b/i;
const CALL_ALLOWED =
	/\bcall\s+(db\.labels|db\.relationshipTypes|db\.propertyKeys|db\.index\.fulltext\.queryNodes)\b/i;
const CALL_ANY = /\bcall\b/i;
const VAR_LEN = /\*\s*(\d*)\s*\.\.\s*(\d*)/g;

function validateReadonly(raw: string): string {
	let q = raw.trim().replace(/;+\s*$/, "");
	if (!q) throw new Error("empty query");
	if (q.includes(";")) throw new Error("multiple statements are not allowed");
	if (WRITE_CLAUSES.test(q)) throw new Error("write clause detected; graph_query is read-only");
	if (CALL_ANY.test(q) && !CALL_ALLOWED.test(q))
		throw new Error("only read-only db.* procedures may be called");
	for (const m of q.matchAll(VAR_LEN)) {
		const upper = m[2] ? Number(m[2]) : 99;
		if (upper > MAX_HOPS) throw new Error(`variable-length pattern exceeds ${MAX_HOPS} hops`);
	}
	if (!/\blimit\s+\d+/i.test(q)) q += `\nLIMIT ${DEFAULT_LIMIT}`;
	return q;
}

export default function activate(pi: ExtensionAPI) {
	let cachedPassword: string | null = null;

	async function neo4jPassword(): Promise<string> {
		if (cachedPassword) return cachedPassword;
		const env = await readFile(join(GRAPH_DIR, ".env"), "utf8");
		const line = env.split("\n").find((l) => l.startsWith("NEO4J_PASSWORD="));
		if (!line) throw new Error("NEO4J_PASSWORD missing from graph/.env");
		cachedPassword = line.slice("NEO4J_PASSWORD=".length).trim();
		return cachedPassword;
	}

	/**
	 * Run Cypher via cypher-shell in the container.
	 *
	 * Values go through `-P` (real query parameters) rather than string
	 * interpolation, so a target name can never be parsed as Cypher.
	 */
	async function cypher(
		query: string,
		params: Record<string, string> = {},
		signal?: AbortSignal,
	): Promise<string> {
		const pw = await neo4jPassword();
		const args = [
			"docker", "exec", "-i", CONTAINER, "cypher-shell",
			"-u", "neo4j", "-p", pw, "--format", "plain",
		];
		for (const [k, v] of Object.entries(params)) args.push("-P", `${k} => ${JSON.stringify(v)}`);
		args.push(query);

		const r = await pi.exec("sudo", args, { timeout: QUERY_TIMEOUT_MS, signal });
		if (r.code !== 0) throw new Error(r.stderr.split("\n").slice(0, 3).join("\n") || "cypher failed");
		return r.stdout.trim();
	}

	async function graphIsUp(): Promise<boolean> {
		try {
			await cypher("RETURN 1");
			return true;
		} catch {
			return false;
		}
	}

	const NOT_RUNNING =
		"The code graph is not running. Start it with:\n" +
		"  cd graph && make up\n" +
		"Then index with: cd graph && make index";

	// ---------------------------------------------------------------- schema
	pi.registerTool({
		name: "graph_schema",
		label: "Graph schema",
		description:
			"Return the literal schema of the code graph: node labels with their " +
			"properties and the relationship patterns that actually exist. Call this " +
			"before writing Cypher so queries only reference real labels.",
		promptSnippet: "Inspect the code graph schema before querying it",
		parameters: Type.Object({}),
		async execute() {
			if (!(await graphIsUp())) return text(NOT_RUNNING);
			const labels = await cypher(
				"MATCH (n) UNWIND labels(n) AS l RETURN l AS label, count(*) AS nodes ORDER BY nodes DESC",
			);
			const props = await cypher(
				"MATCH (n) WITH labels(n)[0] AS label, n LIMIT 4000 UNWIND keys(n) AS k " +
					"RETURN label, collect(DISTINCT k)[0..12] AS properties ORDER BY label",
			);
			const rels = await cypher(
				"MATCH (a)-[r]->(b) RETURN DISTINCT labels(a)[0] AS from, type(r) AS rel, " +
					"labels(b)[0] AS to, count(*) AS n ORDER BY rel",
			);
			return text(`NODE COUNTS\n${labels}\n\nPROPERTIES\n${props}\n\nRELATIONSHIPS\n${rels}`);
		},
	});

	// ----------------------------------------------------------------- query
	pi.registerTool({
		name: "graph_query",
		label: "Graph query",
		description:
			"Run a read-only Cypher query against the code graph. Writes are rejected, " +
			"traversals are capped at 3 hops, and a LIMIT is added when missing. " +
			"Call graph_schema first to learn the real labels and properties.",
		promptSnippet: "Query the code graph with Cypher",
		promptGuidelines: [
			"Use graph_query for precise structural questions (callers, endpoints, table access).",
			"Prefer graph_impact when the question is 'what does changing X affect?'.",
		],
		parameters: Type.Object({
			cypher: Type.String({ description: "read-only Cypher (MATCH / WHERE / RETURN)" }),
		}),
		async execute(_id, { cypher: raw }, signal) {
			if (!(await graphIsUp())) return text(NOT_RUNNING);
			let safe: string;
			try {
				safe = validateReadonly(raw);
			} catch (err) {
				return text(`Rejected: ${(err as Error).message}`);
			}
			try {
				return text((await cypher(safe, {}, signal)) || "(no rows)");
			} catch (err) {
				return text(`Query failed: ${(err as Error).message}`);
			}
		},
	});

	// ---------------------------------------------------------------- impact
	pi.registerTool({
		name: "graph_impact",
		label: "Blast radius",
		description:
			"Show what a change to a table, function, endpoint or file would affect: " +
			"which handlers touch it, which endpoints expose it, and which callers " +
			"reach it transitively. Use before editing unfamiliar code.",
		promptSnippet: "Check the blast radius of a code change",
		parameters: Type.Object({
			target: Type.String({
				description:
					"table name, Go function, endpoint path, React component name, or file path",
			}),
			kind: Type.Optional(
				Type.Union(
					[
						Type.Literal("auto"),
						Type.Literal("table"),
						Type.Literal("func"),
						Type.Literal("endpoint"),
						Type.Literal("component"),
						Type.Literal("file"),
					],
					{ description: "defaults to auto-detect" },
				),
			),
		}),
		async execute(_id, { target, kind = "auto" }, signal) {
			if (!(await graphIsUp())) return text(NOT_RUNNING);
			const t = target.trim();
			const params = { t, tl: t.toLowerCase() };
			const sig = signal;
			const sections: string[] = [];

			const detected =
				kind !== "auto"
					? kind
					: /^(GET|POST|PUT|DELETE|PATCH|ANY)\s/i.test(t) || t.startsWith("/")
						? "endpoint"
						: /\.(go|ts|tsx)$/.test(t)
							? "file"
							: /^[A-Z][A-Za-z0-9_]*$/.test(t)
								? "component"
								: null;

			const add = (title: string, body: string) => {
				const rows = body.trim().split("\n").length - 1;
				if (body.trim() && rows > 0) sections.push(`== ${title} ==\n${body}`);
			};

			if (detected === "table" || detected === null) {
				add(
					"ENDPOINTS REACHING THIS TABLE (<=3 call hops)",
					await cypher(
						"MATCH (tb:Table) WHERE toLower(tb.name) = $tl " +
							"MATCH (tb)<-[acc:READS|WRITES]-(sink:Func)<-[:CALLS*0..3]-(h:Func)<-[:HANDLED_BY]-(e:Endpoint) " +
							"RETURN DISTINCT e.method + ' ' + e.path AS endpoint, type(acc) AS access, " +
							"sink.name AS accessor ORDER BY endpoint LIMIT 60",
						params,
						sig,
					),
				);
				// The cross-stack join: which UI actually breaks if this table
				// changes. A frontend call either merged into the same Endpoint
				// node or points at it via SERVED_BY.
				add(
					"FRONTEND CODE AFFECTED (via the endpoints above)",
					await cypher(
						"MATCH (tb:Table) WHERE toLower(tb.name) = $tl " +
							"MATCH (tb)<-[:READS|WRITES]-(:Func)<-[:HANDLED_BY]-(be:Endpoint) " +
							"OPTIONAL MATCH (fe:Endpoint)-[:SERVED_BY]->(be) " +
							"WITH be, coalesce(fe, be) AS entry " +
							"MATCH (src)-[:FETCHES]->(entry) WHERE src:Component OR src:File " +
							"RETURN DISTINCT coalesce(src.repo, 'n/a') AS repo, " +
							"  labels(src)[0] AS kind, coalesce(src.name, src.path) AS caller, " +
							"  count(DISTINCT be) AS endpoints " +
							"ORDER BY endpoints DESC, caller LIMIT 40",
						params,
						sig,
					),
				);
			}
			if (detected === "func" || detected === null) {
				add(
					"CALLERS (who depends on this function)",
					await cypher(
						"MATCH (f:Func) WHERE toLower(f.name) = $tl OR f.key = $t " +
							"MATCH (c:Func)-[:CALLS]->(f) " +
							"RETURN DISTINCT c.key AS caller, c.file AS file ORDER BY caller LIMIT 40",
						params,
						sig,
					),
				);
				add(
					"WHAT THIS FUNCTION TOUCHES",
					await cypher(
						"MATCH (f:Func) WHERE toLower(f.name) = $tl OR f.key = $t " +
							"OPTIONAL MATCH (f)-[a:READS|WRITES]->(tb:Table) " +
							"OPTIONAL MATCH (f)-[:USES_ENV]->(v:EnvVar) " +
							"RETURN DISTINCT f.key AS fn, type(a) AS access, tb.name AS `table`, " +
							"v.name AS env LIMIT 40",
						params,
						sig,
					),
				);
			}
			if (detected === "endpoint" || detected === null) {
				add(
					"ENDPOINT -> HANDLER -> DATA",
					await cypher(
						"MATCH (e:Endpoint) WHERE toLower(e.path) = $tl OR toLower(e.key) = $tl " +
							"   OR toLower(e.path) CONTAINS $tl " +
							"MATCH (e)-[:HANDLED_BY]->(f:Func) " +
							"OPTIONAL MATCH (f)-[:CALLS*0..2]->(:Func)-[:READS|WRITES]->(tb:Table) " +
							"RETURN DISTINCT e.key AS endpoint, f.key AS handler, f.file AS file, " +
							"   collect(DISTINCT tb.name)[0..10] AS tables LIMIT 30",
						params,
						sig,
					),
				);
			}
			if (detected === "component" || detected === null) {
				// Reverse direction: what data does this piece of UI depend on?
				add(
					"WHAT THIS COMPONENT DEPENDS ON (endpoints -> tables)",
					await cypher(
						"MATCH (c:Component) WHERE toLower(c.name) = $tl OR c.key = $t " +
							"MATCH (c)-[:FETCHES]->(entry:Endpoint) " +
							"OPTIONAL MATCH (entry)-[:SERVED_BY]->(be:Endpoint) " +
							"WITH c, coalesce(be, entry) AS ep " +
							"OPTIONAL MATCH (ep)-[:HANDLED_BY]->(:Func)-[:CALLS*0..2]->(:Func)-[:READS|WRITES]->(tb:Table) " +
							"RETURN DISTINCT c.name AS component, ep.method + ' ' + ep.path AS endpoint, " +
							"   collect(DISTINCT tb.name)[0..8] AS tables LIMIT 40",
						params,
						sig,
					),
				);
				add(
					"WHO RENDERS THIS COMPONENT",
					await cypher(
						"MATCH (c:Component) WHERE toLower(c.name) = $tl OR c.key = $t " +
							"MATCH (parent:Component)-[:RENDERS]->(c) " +
							"RETURN DISTINCT parent.name AS renderedBy, parent.file AS file LIMIT 30",
						params,
						sig,
					),
				);
			}
			if (detected === "file") {
				add(
					"WHAT THIS FILE DECLARES, AND WHO USES IT",
					await cypher(
						"MATCH (fl:File) WHERE fl.path CONTAINS $t " +
							"OPTIONAL MATCH (fl)-[:DECLARES]->(f:Func)<-[:CALLS]-(c:Func) " +
							"WHERE NOT c.file = fl.path " +
							"RETURN DISTINCT fl.path AS file, f.name AS declares, " +
							"   count(DISTINCT c) AS externalCallers ORDER BY externalCallers DESC LIMIT 40",
						params,
						sig,
					),
				);
			}

			return text(
				sections.join("\n\n") ||
					`Nothing in the graph matches "${t}". It may be new code, or the index may be ` +
						"stale — run /graph reindex.",
			);
		},
	});

	// ------------------------------------------------------------------- ask
	pi.registerTool({
		name: "graph_ask",
		label: "Ask the code graph",
		description:
			"Ask a natural-language question about the codebase. A DSPy program " +
			"translates it to Cypher, runs it, and answers with citations from the " +
			"graph. Slower than graph_query; best for exploratory questions.",
		promptSnippet: "Ask the code graph a question in natural language",
		parameters: Type.Object({
			question: Type.String({
				description: "e.g. 'which endpoints write to the bookings table?'",
			}),
		}),
		async execute(_id, { question }, signal) {
			if (!existsSync(VENV_PY)) return text("DSPy env missing. Run: cd graph && make setup");
			if (!(await graphIsUp())) return text(NOT_RUNNING);

			const r = await pi.exec(VENV_PY, [ASK_PY, question], {
				timeout: ASK_TIMEOUT_MS,
				cwd: GRAPH_DIR,
				signal,
			});
			const line = r.stdout.trim().split("\n").filter(Boolean).pop() ?? "";
			if (!line) return text(`graph_ask produced no output. ${r.stderr.slice(-400)}`);

			let parsed: {
				error?: string;
				answer?: string;
				cypher?: string;
				citations?: string[];
				sufficient?: boolean;
				rowCount?: number;
			};
			try {
				parsed = JSON.parse(line);
			} catch {
				return text(`graph_ask returned unparseable output:\n${line.slice(0, 500)}`);
			}
			if (parsed.error) return text(`graph_ask failed: ${parsed.error}`);

			return text(
				[
					parsed.answer ?? "",
					"",
					`--- cypher (${parsed.rowCount ?? 0} rows, sufficient=${parsed.sufficient}) ---`,
					parsed.cypher ?? "",
					parsed.citations?.length
						? `\ncitations: ${parsed.citations.slice(0, 20).join(", ")}`
						: "",
				].join("\n"),
			);
		},
	});

	// ---------------------------------------------------------- orphan calls
	pi.registerTool({
		name: "graph_orphan_calls",
		label: "Unimplemented API calls",
		description:
			"List HTTP calls the frontends make that no Go route serves. These are " +
			"dead calls or unimplemented features: the request 404s at runtime. " +
			"Only findable by comparing both sides of the stack.",
		promptSnippet: "Find frontend API calls with no backend route",
		parameters: Type.Object({
			repo: Type.Optional(
				Type.Union([Type.Literal("all"), Type.Literal("preact"), Type.Literal("backoffice")], {
					description: "defaults to all",
				}),
			),
		}),
		async execute(_id, { repo = "all" }, signal) {
			if (!(await graphIsUp())) return text(NOT_RUNNING);
			const out = await cypher(
				"MATCH (fe:Endpoint) WHERE fe.calledFrom IS NOT NULL " +
					"  AND NOT exists((fe)-[:HANDLED_BY]->()) AND NOT exists((fe)-[:SERVED_BY]->()) " +
					"MATCH (src)-[r:FETCHES]->(fe) " +
					"WHERE $repo = 'all' OR coalesce(src.repo, '') = $repo " +
					"RETURN fe.key AS unservedCall, coalesce(src.name, src.path) AS calledFrom, " +
					"  r.line AS line ORDER BY unservedCall LIMIT 80",
				{ repo },
				signal,
			);
			const rows = out.trim().split("\n").length - 1;
			return text(
				rows > 0
					? `${out}\n\n${rows} frontend call site(s) have no matching backend route.`
					: "Every frontend API call resolves to a backend route.",
			);
		},
	});

	// -------------------------------------------------- post-edit blast radius
	//
	// The value of the graph is knowing the consequences of a change. This
	// appends them to the edit result, where they are impossible to miss.
	pi.on("tool_result", async (event) => {
		if (event.toolName !== "edit" && event.toolName !== "write") return;
		if (event.isError) return;
		const path = (event.input as { path?: string } | undefined)?.path;
		if (!path || !/\.(go|ts|tsx)$/.test(path)) return;

		const rel = path.replace(`${REPO_ROOT}/`, "");
		try {
			if (!(await graphIsUp())) return;

			const isGo = path.endsWith(".go");
			const out = isGo
				? await cypher(
						"MATCH (fl:File) WHERE fl.path ENDS WITH $p " +
							"MATCH (fl)-[:DECLARES]->(f:Func) " +
							"OPTIONAL MATCH (f)<-[:HANDLED_BY]-(e:Endpoint) " +
							"OPTIONAL MATCH (f)-[:READS|WRITES]->(tb:Table) " +
							"OPTIONAL MATCH (f)<-[:CALLS]-(c:Func) WHERE NOT c.file = fl.path " +
							"RETURN count(DISTINCT e) AS endpoints, count(DISTINCT c) AS externalCallers, " +
							"  collect(DISTINCT tb.name)[0..8] AS tables LIMIT 1",
						{ p: rel },
					)
				: // A frontend file matters through what it calls and who renders it.
					await cypher(
						"MATCH (fl:File) WHERE fl.path ENDS WITH $p " +
							"OPTIONAL MATCH (fl)-[:DECLARES]->(c:Component)<-[:RENDERS]-(parent:Component) " +
							"OPTIONAL MATCH (fl)-[:DECLARES]->(:Component)-[:FETCHES]->(e1:Endpoint) " +
							"OPTIONAL MATCH (fl)-[:FETCHES]->(e2:Endpoint) " +
							"RETURN count(DISTINCT parent) AS renderedBy, " +
							"  count(DISTINCT e1) + count(DISTINCT e2) AS endpointsCalled LIMIT 1",
						{ p: rel },
					);

			const values = out.split("\n")[1]?.trim();
			// Header only, or a file nothing depends on: nothing worth saying.
			if (!values || /^0, 0(, \[\])?$/.test(values)) return;

			const shape = isGo
				? "[endpoints, externalCallers, tables]"
				: "[renderedBy, endpointsCalled]";
			return {
				content: [
					...event.content,
					{
						type: "text" as const,
						text:
							`Code graph — ${rel} ${shape} = ${values}. ` +
							"Run graph_impact on the changed symbol before assuming this edit is local. " +
							"After finishing, /graph reindex to keep the graph current.",
					},
				],
			};
		} catch {
			// The graph is an aid, never a blocker.
			return;
		}
	});

	// --------------------------------------------------------------- /graph
	pi.registerCommand("graph", {
		description: "Code graph: status, reindex",
		getArgumentCompletions: (prefix) => {
			const subs = ["status", "reindex"].filter((s) => s.startsWith(prefix));
			return subs.length ? subs.map((s) => ({ value: s, label: s })) : null;
		},
		handler: async (args, ctx) => {
			const sub = args.trim() || "status";

			if (sub === "status") {
				if (!(await graphIsUp())) {
					ctx.ui.notify(NOT_RUNNING, "warning");
					return;
				}
				const counts = await cypher(
					"MATCH (n) WITH count(n) AS nodes MATCH ()-[r]->() RETURN nodes, count(r) AS rels",
				);
				const byLabel = await cypher(
					"MATCH (n) UNWIND labels(n) AS l RETURN l, count(*) AS c ORDER BY c DESC LIMIT 12",
				);
				ctx.ui.notify(
					`${counts}\n\n${byLabel}\n\nDSPy prompts: ` +
						`${existsSync(COMPILED) ? "compiled" : "not compiled (run make optimize)"}`,
					"info",
				);
				return;
			}

			if (sub === "reindex") {
				ctx.ui.notify("Re-extracting the Go code graph...", "info");
				const r = await pi.exec("make", ["index"], { cwd: GRAPH_DIR, timeout: 600_000 });
				const tail = (t: string, n: number) => t.trim().split("\n").slice(-n).join("\n");
				ctx.ui.notify(
					r.code === 0 ? `${tail(r.stderr, 6)}\n${tail(r.stdout, 3)}` : `Reindex failed:\n${tail(r.stderr, 10)}`,
					r.code === 0 ? "info" : "error",
				);
				return;
			}

			ctx.ui.notify("Usage: /graph [status|reindex]", "info");
		},
	});
}
