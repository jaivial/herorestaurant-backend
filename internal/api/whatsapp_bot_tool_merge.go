package api

// Coordination id: wa_bot_privileged_group_v1
//
// The privileged turn offers the bot its own tools PLUS the whole forky
// catalog. The two catalogs overlap (create_booking exists in both), and the
// provider rejects a tool list with duplicate names:
//
//	opencode-go http 400: Tool names must be unique.
//
// That killed the primary model on every privileged turn and forced the
// fallback, which made the group bot slow and sometimes silent. These
// helpers build a list with no repeated name, keeping the FIRST definition of
// each name: the bot's own tool comes first, so the bot keeps its schema and
// its executor, and the forky tool of the same name is simply not offered.

// botMergeToolDefs concatenates two tool lists keeping only the first
// definition of each name.
func botMergeToolDefs(lists ...[]botToolDef) []botToolDef {
	seen := map[string]struct{}{}
	out := make([]botToolDef, 0, 32)
	for _, list := range lists {
		for _, d := range list {
			name := d.Name
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}
