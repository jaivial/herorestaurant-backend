package api

import "github.com/google/uuid"

// Coordination id: sql_uuid_v1_pg_v1
//
// MySQL UUID() returns an RFC 4122 version-1 UUID (timestamp + clock
// sequence + node) as 36-char lowercase text. PG core has no v1 generator and
// the import plan creates no extension, so the PG twins bind a v1 UUID made by
// google/uuid (NewUUID: RFC 4122 v1, 100ns timestamp, clock sequence, node =
// a host interface MAC or a random node) at the actual runtime call. Consumers
// audited (ticket_number "F-<uuid>", site_builder_domain_mappings.id) only rely
// on uniqueness/opaque text: no ordering, timestamp or MAC extraction. The node
// differs from the MySQL server's node, so values are not byte-identical
// across engines (format and version are). Generation failure is returned:
// never an empty id. The MySQL branches keep UUID() in SQL (unchanged).
func sqlUUIDv1() (string, error) {
	u, err := uuid.NewUUID()
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
