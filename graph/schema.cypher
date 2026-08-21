// Schema for the newvillacarmen code graph.
// Idempotent: safe to re-run. Apply with `make schema`.
//
// Every node has a stable `key` (or natural unique property) so the loader can
// MERGE instead of CREATE and stay idempotent across re-indexes.
//
// Key formats (built by the extractor, never guessed):
//   Package.importPath  "preactvillacarmen/internal/api"
//   File.path           "backend/internal/api/bookings.go"   (repo-prefixed)
//   Func.key            "preactvillacarmen/internal/api.(*Server).handleBooking"
//   Type.key            "preactvillacarmen/internal/api.Server"
//   Endpoint.key        "GET /api/public/booking"
//   Column.key          "bookings.party_size"
//   Component.key       "backoffice:BookingCard"      (repo-qualified)
//   Route.key           "preact:/reservar"            (repo-qualified)

// --- uniqueness constraints (also create backing indexes) -------------------
CREATE CONSTRAINT repo_name IF NOT EXISTS
  FOR (n:Repo) REQUIRE n.name IS UNIQUE;
CREATE CONSTRAINT file_path IF NOT EXISTS
  FOR (n:File) REQUIRE n.path IS UNIQUE;
CREATE CONSTRAINT package_import IF NOT EXISTS
  FOR (n:Package) REQUIRE n.importPath IS UNIQUE;
CREATE CONSTRAINT func_key IF NOT EXISTS
  FOR (n:Func) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT type_key IF NOT EXISTS
  FOR (n:Type) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT endpoint_key IF NOT EXISTS
  FOR (n:Endpoint) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT table_name IF NOT EXISTS
  FOR (n:Table) REQUIRE n.name IS UNIQUE;
CREATE CONSTRAINT column_key IF NOT EXISTS
  FOR (n:Column) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT envvar_name IF NOT EXISTS
  FOR (n:EnvVar) REQUIRE n.name IS UNIQUE;
CREATE CONSTRAINT doc_key IF NOT EXISTS
  FOR (n:Doc) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT component_key IF NOT EXISTS
  FOR (n:Component) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT route_key IF NOT EXISTS
  FOR (n:Route) REQUIRE n.key IS UNIQUE;

// --- lookup indexes for the hot query paths --------------------------------
CREATE INDEX func_name IF NOT EXISTS FOR (n:Func) ON (n.name);
CREATE INDEX func_file IF NOT EXISTS FOR (n:Func) ON (n.file);
CREATE INDEX type_name IF NOT EXISTS FOR (n:Type) ON (n.name);
CREATE INDEX endpoint_path IF NOT EXISTS FOR (n:Endpoint) ON (n.path);
CREATE INDEX file_repo IF NOT EXISTS FOR (n:File) ON (n.repo);
CREATE INDEX package_repo IF NOT EXISTS FOR (n:Package) ON (n.repo);
CREATE INDEX component_name IF NOT EXISTS FOR (n:Component) ON (n.name);
CREATE INDEX component_repo IF NOT EXISTS FOR (n:Component) ON (n.repo);
// The join column between frontend calls and backend routes: parameter names
// erased, so ${menuId} on one side matches {id} on the other.
CREATE INDEX endpoint_canon IF NOT EXISTS FOR (n:Endpoint) ON (n.canon);

// Full-text over symbol names: the fuzzy-matching half of layer 5 retrieval,
// so we do not need embeddings just to resolve "the booking handler".
CREATE FULLTEXT INDEX symbol_search IF NOT EXISTS
  FOR (n:Func|Type|Endpoint|File|Table|Component|Route) ON EACH [n.name, n.path, n.key];
