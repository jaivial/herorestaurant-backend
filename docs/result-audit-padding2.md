# result-audit-padding2

- **Audit:** `docs/dbmigrate/collation-padding-audit-2.md` covers 30 keys: 8 contact, then 22 code. No pure-opaque key fit in this batch. Each key has exactly one status, with `file:line` evidence. No key is marked as having full parity.
- **Key statuses (30, disjoint):**

  | Status | Count |
  |---|---|
  | code-canonical | 9 |
  | code-canonical, one path unverified | 2 |
  | generated | 6 |
  | enum | 2 (rows 13 and 28; a third enum, accounting_exports.export_type, is part of row 9) |
  | no runtime Go writer | 4 |
  | PAD-sensitive probe | 2 |
  | not verified | 3 |
  | opaque provider ref already TrimSpace'd by source | 1 |
  | sku via stockNullableString | 1 |

- **Remedies:** each gap or partial key has a value-preserving remedy (an rtrim expression index with explicit `COLLATE dbmigrate.ci_ai`, plus the exact ON CONFLICT and lookup changes). IDs are not blanket-trimmed.
- **Four human GAP keys:** unapplied index and caller proposals, with values retained.
- **Code change (bounded safety):** `site_builder.go` site update. A non-empty `custom_domain` is now checked with the existing `domainOwnedByOther(..., site.RestaurantID)` before the update.
  - A domain owned by another restaurant returns 409; a lookup error returns 500 (fail closed).
  - It creates or activates no mapping, route or DNS proof (custom_domain is display-only; its readers are `site_builder.go:206/1851` and `whatsapp_bot_admin_reads.go:800`).
  - Build: `go build -p 2 ./...` exits 0. No SQL site changed, so the inventory stays at r95 (2499).
- **Legacy padded rows:** canonical writers do not stop a PG claim of an identity whose imported owner row is padded. There is an unapplied proposal for read-only pre-checks, global rtrim expression indexes and expression-aware `restaurantDomainOwner` and ON CONFLICT targets. Domain ownership stays gated for PG activation.
- **UAZAPI:** the refusal happens after the instance is provisioned at the provider. There are no network calls and no automatic cleanup; the existing orphan gate is unchanged.
- **Unchanged:** checkout stays at 503; R95 is syntax coverage only, with 17 legitimate MySQL-only sites; 0 full handler QA; the 1117 pending items and 731 orphans are unwaived; R5 is untouched; the PG server is disabled; tools are report-only.
- **Remaining:** 63 keys are not audited, and 5 items are open from this batch.
- **Next:**
  1. Chunk 3 (remaining code keys and opaque keys).
  2. The open items.
  3. Read-only padded-row pre-check queries for the coordinator.

