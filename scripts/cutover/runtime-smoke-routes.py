#!/usr/bin/env python3
"""runtime-smoke-routes.py - list the chi routes of internal/api/server.go.

Prints TSV `METHOD<TAB>/api/path` (the public prefix the server rewrites:
/admin/* is reached as /api/admin/*, the rest as /api/*). Tracks nested
r.Route("/prefix", func...) blocks by brace depth; braces inside string
literals (route params like {id}) are ignored. Read-only.
Usage: runtime-smoke-routes.py <src-root>
"""
import os, re, sys

src = open(os.path.join(sys.argv[1], "internal/api/server.go"), encoding="utf-8").read()
start = src.index("func (s *Server) Routes() http.Handler")
body = src[start:]

str_re = re.compile(r'"(?:[^"\\\n]|\\.)*"|`[^`]*`')
route_re = re.compile(r'\.Route\("([^"]+)"')
meth_re = re.compile(r'\.(Get|Post|Put|Patch|Delete)\("([^"]+)"')

depth, stack, out = 0, [], set()
for line in body.splitlines():
    code = line.split("//", 1)[0] if not line.strip().startswith("//") else ""
    if not code:
        continue
    prefix = "".join(p for p, _ in stack)
    for m in meth_re.finditer(code):
        out.add((m.group(1).upper(), prefix + m.group(2)))
    rm = route_re.search(code)
    bare = str_re.sub('""', code)
    opens, closes = bare.count("{"), bare.count("}")
    if rm:
        stack.append((rm.group(1), depth))
    depth += opens - closes
    while stack and depth <= stack[-1][1]:
        stack.pop()
    if depth <= 0 and "}" in bare:  # end of Routes()
        break

for meth, path in sorted(out, key=lambda x: (x[1], x[0])):
    pub = "/api" + path if not path.startswith("/api") else path
    print(f"{meth}\t{pub}")
