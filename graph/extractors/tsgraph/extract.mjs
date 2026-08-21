/**
 * tsgraph — extracts a code graph from the TypeScript/TSX frontends.
 *
 * Emits the same triples.jsonl shape as extractors/gograph, so the loader and
 * every query work across the whole stack. The payoff is one join:
 *
 *     (:Component)-[:FETCHES]->(:Endpoint)-[:HANDLED_BY]->(:Func)-[:WRITES]->(:Table)
 *
 * which turns "change a column" into "these React components break".
 *
 * Deterministic: ts-morph resolves the AST, so nothing is guessed. Where a
 * call path cannot be resolved statically (a URL built at runtime) the fact is
 * omitted rather than invented.
 *
 * Extracted:
 *   File, Module, Component, Route      nodes
 *   IN, IMPORTS, DECLARES               structure
 *   RENDERS                             component -> component (JSX usage)
 *   FETCHES                             component/file -> endpoint (HTTP calls)
 *   MOUNTS                              route -> component
 *
 * Usage:
 *   node extract.mjs --root /var/www/newvillacarmen/backoffice \
 *                    --repo backoffice --out ../../out/triples.backoffice.jsonl
 */

import { writeFileSync, existsSync } from "node:fs";
import { basename, join, relative, resolve } from "node:path";
import { Node, Project, SyntaxKind } from "ts-morph";

// --------------------------------------------------------------------- output

const seenNodes = new Set();
const seenEdges = new Set();
const lines = [];
let nodeCount = 0;
let edgeCount = 0;

function emitNode(label, key, props = {}) {
	if (!key) return;
	const id = `${label}\u0000${key}`;
	if (seenNodes.has(id)) return;
	seenNodes.add(id);
	nodeCount++;
	lines.push(JSON.stringify({ kind: "node", label, key, props }));
}

function emitEdge(type, fromLabel, fromKey, toLabel, toKey, props = {}) {
	if (!fromKey || !toKey) return;
	const id = [type, fromLabel, fromKey, toLabel, toKey].join("\u0000");
	if (seenEdges.has(id)) return;
	seenEdges.add(id);
	edgeCount++;
	lines.push(
		JSON.stringify({ kind: "edge", type, fromLabel, fromKey, toLabel, toKey, props }),
	);
}

// ------------------------------------------------------------ path normalizing

/**
 * Turn a frontend URL into the backend's endpoint path.
 *
 * The backend mounts routes without the `/api` prefix (a middleware strips it),
 * so `/api/admin/vinos/${id}` must become `/admin/vinos/{param}` before it can
 * be matched against a chi route.
 */
export function normalizeApiPath(raw) {
	if (typeof raw !== "string" || !raw.startsWith("/")) return null;
	let p = raw.replace(/\?[\s\S]*$/, "").replace(/#[\s\S]*$/, "");
	p = p.replace(/^\/api(?=\/|$)/, "");
	if (p === "") p = "/";
	// A hole preceded by `/` is a whole path segment, i.e. a real parameter.
	p = p.replace(/(?<=\/)\$\{[^}]*\}/g, "{param}");
	// A hole *not* preceded by `/` continues the previous segment, and in
	// practice holds a query suffix — `/admin/tables${suffix ? `?…` : ""}`.
	// Neither is part of the route, so drop it and anything after.
	p = p.replace(/\$\{[\s\S]*$/, "");
	p = p.replace(/\/{2,}/g, "/");
	if (p.length > 1) p = p.replace(/\/+$/, "");
	return p || "/";
}

/**
 * Parameter-name-insensitive form used to join the two sides of the stack.
 *
 * The frontend writes `${menuId}` where the backend declares `{id}`; both mean
 * "one path segment". Erasing the names lets them match without inventing a
 * correspondence between variable names.
 */
export function canonicalPath(p) {
	if (!p) return null;
	const c = p.replace(/\{[^}]*\}/g, "{}").replace(/\/+$/, "");
	return c || "/";
}

// --------------------------------------------------------------- URL literals

/**
 * Recover a static path from a string or template literal.
 *
 * Template holes become `${}` markers rather than being dropped, so
 * `/admin/x/${id}` stays distinguishable from `/admin/x`.
 */
function literalPath(node) {
	if (!node) return null;
	if (Node.isStringLiteral(node) || Node.isNoSubstitutionTemplateLiteral(node)) {
		return node.getLiteralText();
	}
	if (Node.isTemplateExpression(node)) {
		let out = node.getHead().getLiteralText();
		for (const span of node.getTemplateSpans()) {
			out += "${}" + span.getLiteral().getLiteralText();
		}
		return out;
	}
	// `"/admin/" + id` — keep the static prefix, mark the rest as dynamic.
	if (Node.isBinaryExpression(node) && node.getOperatorToken().getText() === "+") {
		const left = literalPath(node.getLeft());
		if (left) return left.endsWith("/") ? `${left}\${}` : left;
	}
	return null;
}

// HTTP verb inference. The frontend usually passes `{ method: "POST" }`; a
// bare call is a GET.
const VERBS = new Set(["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]);

function methodFromArgs(args) {
	for (const arg of args.slice(1)) {
		if (!Node.isObjectLiteralExpression(arg)) continue;
		for (const prop of arg.getProperties()) {
			if (!Node.isPropertyAssignment(prop)) continue;
			if (prop.getName().replace(/["']/g, "") !== "method") continue;
			const init = prop.getInitializer();
			const v = init && literalPath(init);
			const verb = (v ?? init?.getText() ?? "").replace(/["'`]/g, "").toUpperCase();
			if (VERBS.has(verb)) return verb;
		}
	}
	return null;
}

/**
 * Callees whose first argument is a URL, mapped to the verb they imply.
 *
 * `null` means "look at the options object" — a bare fetch/apiFetch defaults to
 * GET unless `{ method }` says otherwise. Playwright's `request.post()` and
 * similar carry the verb in the method name itself.
 */
const FETCH_NAMES = new Map([
	["fetch", null],
	["apiFetch", null],
	["apiUrl", null],
	["json", null],
	["jsonWithFallback", null],
	["request", null],
	["get", "GET"],
	["post", "POST"],
	["put", "PUT"],
	["patch", "PATCH"],
	["del", "DELETE"],
	["delete", "DELETE"],
]);

/**
 * Find wrappers that prefix a base path onto their argument.
 *
 * `stockItemApi.ts` exports `request(path)` whose body calls
 * fetch(`/api/admin/stock${path}`). Callers pass `/items/${id}`, so without
 * resolving the wrapper every one of its calls records a path that no backend
 * route matches. Relative imports are followed because the wrapper is
 * routinely declared in a sibling module rather than at the call site.
 *
 * Returns a map of local binding name -> base prefix.
 */
function findPrefixWrappers(sf, depth = 0) {
	const wrappers = new Map();

	const inspect = (name, fn) => {
		if (!name || !fn) return;
		for (const call of fn.getDescendantsOfKind(SyntaxKind.CallExpression)) {
			const callee = call.getExpression();
			const cname = Node.isPropertyAccessExpression(callee)
				? callee.getName()
				: callee.getText();
			if (!FETCH_NAMES.has(cname)) continue;
			const arg = call.getArguments()[0];
			if (!arg || !Node.isTemplateExpression(arg)) continue;
			// `/api/admin/stock${path}` — head is the prefix, and the single hole
			// must be one of the wrapper's own parameters.
			const head = arg.getHead().getLiteralText();
			const spans = arg.getTemplateSpans();
			if (spans.length !== 1 || !head.startsWith("/")) continue;
			if (spans[0].getLiteral().getLiteralText() !== "") continue;
			const params = fn.getParameters().map((p) => p.getName());
			if (!params.includes(spans[0].getExpression().getText())) continue;
			// The wrapper usually fixes the verb too (platformPost sets
			// method:"POST"); without it every call would be recorded as GET.
			wrappers.set(name, {
				prefix: head,
				verb: methodFromArgs(call.getArguments()) ?? FETCH_NAMES.get(cname) ?? null,
			});
			return;
		}
	};

	for (const fn of sf.getFunctions()) inspect(fn.getName(), fn);
	for (const v of sf.getVariableDeclarations()) {
		const init = v.getInitializer();
		if (init && (Node.isArrowFunction(init) || Node.isFunctionExpression(init))) {
			inspect(v.getName(), init);
		}
	}

	// One level of relative imports: enough for the sibling-module pattern
	// without walking the whole dependency graph on every file.
	if (depth === 0) {
		for (const imp of sf.getImportDeclarations()) {
			const spec = imp.getModuleSpecifierValue();
			if (!spec.startsWith(".")) continue;
			const target = imp.getModuleSpecifierSourceFile();
			if (!target) continue;
			const exported = findPrefixWrappers(target, 1);
			if (exported.size === 0) continue;
			for (const named of imp.getNamedImports()) {
				const original = named.getNameNode().getText();
				const local = named.getAliasNode()?.getText() ?? original;
				const w = exported.get(original);
				if (w && !wrappers.has(local)) wrappers.set(local, w);
			}
		}
	}
	return wrappers;
}

// ------------------------------------------------------------------ components

/** A PascalCase function/const returning JSX is a component. */
function isComponentName(name) {
	return !!name && /^[A-Z][A-Za-z0-9_]*$/.test(name);
}

function enclosingComponent(node, componentRanges) {
	const pos = node.getStart();
	let best = null;
	for (const c of componentRanges) {
		if (pos >= c.start && pos <= c.end) {
			if (!best || c.start > best.start) best = c;
		}
	}
	return best;
}

// ----------------------------------------------------------------------- main

function parseArgs() {
	const out = { root: ".", repo: "frontend", out: "triples.ts.jsonl" };
	const argv = process.argv.slice(2);
	for (let i = 0; i < argv.length; i++) {
		const k = argv[i].replace(/^--/, "");
		if (k in out) out[k] = argv[++i];
	}
	return out;
}

function main() {
	const { root, repo, out } = parseArgs();
	const absRoot = resolve(root);
	if (!existsSync(absRoot)) {
		console.error(`tsgraph: root not found: ${absRoot}`);
		process.exit(1);
	}

	const tsconfig = ["tsconfig.json", "tsconfig.app.json"]
		.map((f) => join(absRoot, f))
		.find((f) => existsSync(f));

	const project = new Project({
		...(tsconfig ? { tsConfigFilePath: tsconfig } : {}),
		skipAddingFilesFromTsConfig: true,
		compilerOptions: { allowJs: false, jsx: 2 /* react */ },
	});

	// Explicit globs: tsconfig include lists vary between the two repos, and a
	// missed file is a missing edge. Tests and config are excluded: they call
	// endpoints the application does not, which would inflate blast radius with
	// paths no user can reach.
	project.addSourceFilesAtPaths([
		join(absRoot, "**/*.ts"),
		join(absRoot, "**/*.tsx"),
		`!${join(absRoot, "**/node_modules/**")}`,
		`!${join(absRoot, "**/dist/**")}`,
		`!${join(absRoot, "**/build/**")}`,
		`!${join(absRoot, "**/.vite/**")}`,
		`!${join(absRoot, "**/e2e/**")}`,
		`!${join(absRoot, "**/tests/**")}`,
		`!${join(absRoot, "**/__tests__/**")}`,
		`!${join(absRoot, "**/*.spec.ts")}`,
		`!${join(absRoot, "**/*.spec.tsx")}`,
		`!${join(absRoot, "**/*.test.ts")}`,
		`!${join(absRoot, "**/*.test.tsx")}`,
		`!${join(absRoot, "**/*.config.ts")}`,
		`!${join(absRoot, "**/*.d.ts")}`,
	]);

	const files = project.getSourceFiles();
	emitNode("Repo", repo, { name: repo, lang: "typescript" });

	const rel = (p) => `${repo}/${relative(absRoot, p).split("\\").join("/")}`;

	// Component key is repo-qualified: two repos may both export `Layout`.
	const componentKey = (name, filePath) => `${repo}:${name}`;

	// Pass 1: files, components, routes.
	const declaredComponents = new Map(); // name -> key
	for (const sf of files) {
		const path = rel(sf.getFilePath());
		emitNode("File", path, {
			path,
			repo,
			lang: sf.getFilePath().endsWith(".tsx") ? "tsx" : "ts",
			loc: sf.getEndLineNumber(),
		});
		emitEdge("IN", "File", path, "Repo", repo);

		for (const imp of sf.getImportDeclarations()) {
			const spec = imp.getModuleSpecifierValue();
			// Bare specifiers are packages; relative ones are internal files and
			// would duplicate the File nodes we already emit.
			if (spec.startsWith(".") || spec.startsWith("/")) continue;
			emitNode("Module", spec, { specifier: spec });
			emitEdge("IMPORTS", "File", path, "Module", spec);
		}

		const ranges = [];
		const addComponent = (name, node) => {
			if (!isComponentName(name)) return;
			const key = componentKey(name, path);
			const line = node.getStartLineNumber();
			emitNode("Component", key, { key, name, file: path, repo, line });
			emitEdge("DECLARES", "File", path, "Component", key, { line });
			declaredComponents.set(name, key);
			ranges.push({ key, name, start: node.getStart(), end: node.getEnd() });
		};

		for (const fn of sf.getFunctions()) {
			const name = fn.getName();
			if (name && containsJsx(fn)) addComponent(name, fn);
		}
		for (const v of sf.getVariableDeclarations()) {
			const init = v.getInitializer();
			if (!init) continue;
			if (
				(Node.isArrowFunction(init) || Node.isFunctionExpression(init)) &&
				containsJsx(init)
			) {
				addComponent(v.getName(), v);
			}
		}
		// Vike pages export default; the file path is the identity.
		const def = sf.getDefaultExportSymbol();
		if (def && ranges.length === 0 && sf.getFilePath().endsWith(".tsx")) {
			const name = basename(sf.getFilePath()).replace(/\.tsx$/, "");
			if (name.startsWith("+")) {
				const key = `${repo}:${relative(absRoot, sf.getFilePath())}`;
				emitNode("Component", key, {
					key,
					name,
					file: path,
					repo,
					line: 1,
					isPage: true,
				});
				emitEdge("DECLARES", "File", path, "Component", key, { line: 1 });
				ranges.push({ key, name, start: 0, end: sf.getEnd() });
			}
		}

		sf.__ranges = ranges;
	}

	// Pass 2: JSX usage (RENDERS), fetches (FETCHES), routes (MOUNTS).
	for (const sf of files) {
		const path = rel(sf.getFilePath());
		const ranges = sf.__ranges ?? [];
		const localWrappers = findPrefixWrappers(sf);

		sf.forEachDescendant((node) => {
			// --- RENDERS: <Child /> inside a component
			if (
				Node.isJsxOpeningElement(node) ||
				Node.isJsxSelfClosingElement(node)
			) {
				const tag = node.getTagNameNode().getText();
				if (isComponentName(tag)) {
					const child = declaredComponents.get(tag);
					const parent = enclosingComponent(node, ranges);
					if (child && parent && child !== parent.key) {
						emitEdge("RENDERS", "Component", parent.key, "Component", child, {
							line: node.getStartLineNumber(),
						});
					}
				}
				// --- MOUNTS: wouter <Route path="/x" component={Y} />
				if (tag === "Route") {
					let routePath = null;
					let compName = null;
					for (const attr of node.getAttributes()) {
						if (!Node.isJsxAttribute(attr)) continue;
						const an = attr.getNameNode().getText();
						const init = attr.getInitializer();
						if (an === "path") {
							routePath =
								(init && Node.isStringLiteral(init) && init.getLiteralText()) ||
								(init &&
									Node.isJsxExpression(init) &&
									literalPath(init.getExpression())) ||
								null;
						} else if (an === "component" && init && Node.isJsxExpression(init)) {
							compName = init.getExpression()?.getText() ?? null;
						}
					}
					if (routePath) {
						const rkey = `${repo}:${routePath}`;
						emitNode("Route", rkey, {
							path: routePath,
							key: rkey,
							repo,
							file: path,
							line: node.getStartLineNumber(),
						});
						emitEdge("IN", "Route", rkey, "Repo", repo);
						const target = compName && declaredComponents.get(compName);
						if (target) emitEdge("MOUNTS", "Route", rkey, "Component", target);
					}
				}
			}

			// --- FETCHES: any call whose first argument is a URL path
			if (Node.isCallExpression(node)) {
				const callee = node.getExpression();
				const name = Node.isPropertyAccessExpression(callee)
					? callee.getName()
					: callee.getText();
				// A module-local wrapper is itself a fetch site, even though its
				// name is arbitrary (stockRequest, posRequest, request...).
				const wrapper = localWrappers.get(name);
				if (!FETCH_NAMES.has(name) && wrapper === undefined) return;

				const args = node.getArguments();
				let raw = literalPath(args[0]);
				// A wrapper's own body — fetch(`/api${path}`) — describes how the
				// wrapper builds URLs, not a call to `/api`. Its callers are
				// recorded instead, with the prefix re-attached.
				if (
					raw &&
					Node.isTemplateExpression(args[0]) &&
					args[0].getTemplateSpans().length === 1 &&
					raw.endsWith("${}")
				) {
					return;
				}
				// Re-attach the base path when the call goes through a wrapper,
				// otherwise the recorded route is missing its prefix.
				if (wrapper && raw?.startsWith("/")) raw = wrapper.prefix + raw;
				const p = normalizeApiPath(raw);
				if (!p) return;

				const canon = canonicalPath(p);
				// Verb precedence: an explicit `{ method }` beats the callee name,
				// which beats the GET default.
				// Verb precedence: an explicit `{ method }` at the call site, then the
				// verb the wrapper hardcodes, then the callee name, then GET.
				const verb =
					methodFromArgs(args) ?? wrapper?.verb ?? FETCH_NAMES.get(name) ?? "GET";
				// The endpoint key stays the backend's (verb + path); `canon` is the
				// join column, resolved by the loader.
				const ekey = `${verb} ${p}`;
				emitNode("Endpoint", ekey, {
					key: ekey,
					method: verb,
					path: p,
					canon,
					calledFrom: repo,
				});

				const owner = enclosingComponent(node, ranges);
				const props = { line: node.getStartLineNumber(), via: name };
				if (owner) {
					emitEdge("FETCHES", "Component", owner.key, "Endpoint", ekey, props);
				} else {
					emitEdge("FETCHES", "File", path, "Endpoint", ekey, props);
				}
			}
		});
	}

	// Vike filesystem routing: pages/admin/vinos/+Page.tsx -> /admin/vinos
	for (const sf of files) {
		const fp = sf.getFilePath();
		if (!/\/\+Page\.tsx$/.test(fp)) continue;
		const r = relative(absRoot, fp);
		const m = r.match(/^(?:src\/)?pages\/(.*)\/\+Page\.tsx$/);
		if (!m) continue;
		// Vike marks parameters with @slug and groups with (group).
		const routePath =
			"/" +
			m[1]
				.split("/")
				.filter((seg) => !/^\(.*\)$/.test(seg))
				.map((seg) => (seg.startsWith("@") ? `{${seg.slice(1)}}` : seg))
				.filter((seg) => seg !== "index")
				.join("/");
		const clean = routePath.replace(/\/+$/, "") || "/";
		const rkey = `${repo}:${clean}`;
		emitNode("Route", rkey, { path: clean, key: rkey, repo, file: rel(fp), line: 1 });
		emitEdge("IN", "Route", rkey, "Repo", repo);
		const ckey = `${repo}:${r}`;
		if (seenNodes.has(`Component\u0000${ckey}`)) {
			emitEdge("MOUNTS", "Route", rkey, "Component", ckey);
		}
	}

	writeFileSync(out, lines.join("\n") + "\n");
	console.error(
		`tsgraph: repo=${repo} files=${files.length} nodes=${nodeCount} edges=${edgeCount} -> ${out}`,
	);
}

/** True if the function body contains JSX, i.e. it renders something. */
function containsJsx(fn) {
	return (
		fn.getFirstDescendantByKind(SyntaxKind.JsxElement) !== undefined ||
		fn.getFirstDescendantByKind(SyntaxKind.JsxSelfClosingElement) !== undefined ||
		fn.getFirstDescendantByKind(SyntaxKind.JsxFragment) !== undefined
	);
}

main();
