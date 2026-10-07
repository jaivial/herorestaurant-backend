package api

import (
	"database/sql"
	"strconv"
	"strings"
)

// =============================================================================
// Menu type domain - the single source of truth for menus.menu_type and
// restaurant_menu_templates.menu_type, both stored as a numeric code.
//
// The column used to be VARCHAR holding loose strings, compared literally in
// dozens of SQL statements. It is now a TINYINT with the documented codes
// below (migration 165_menu_type_numeric.sql); the REST API answers with the
// numeric code and still accepts the legacy string on the way in, so backend
// and frontends can be rolled out independently.
//
// WHAT EACH NUMERIC VALUE MEANS
//
//	0 = no type / unknown (reserved for a missing or unrecognized value, so a
//	    bad row stays visible instead of being silently rewritten as the
//	    default). It is never written on purpose.
//	1 = closed_conventional  - closed menu, conventional (the current default)
//	2 = closed_group         - closed menu for groups
//	3 = a_la_carte           - carta / a la carta, conventional
//	4 = a_la_carte_group     - carta for groups
//	5 = a_la_carte_time      - carta by time slot
//	6 = special              - special menu (season / event)
//
// The code 1 is applied ONLY to a row whose source value was really empty; an
// unknown value is kept as 0 on purpose so it can be detected and fixed.
//
// Coordination id: menu_type_numeric_v1
// (DB menus.menu_type / restaurant_menu_templates.menu_type <-> REST payloads
// <-> backoffice + preact consumers)
// =============================================================================

// Canonical menu type codes.
const (
	// MenuTypeUnknown is the reserved code for a missing or unrecognized value.
	MenuTypeUnknown int = 0
	// MenuTypeClosedConventional is the closed conventional menu (default).
	MenuTypeClosedConventional int = 1
	// MenuTypeClosedGroup is the closed group menu.
	MenuTypeClosedGroup int = 2
	// MenuTypeALaCarte is the conventional carta menu.
	MenuTypeALaCarte int = 3
	// MenuTypeALaCarteGroup is the group carta menu.
	MenuTypeALaCarteGroup int = 4
	// MenuTypeALaCarteTime is the carta by time slot.
	MenuTypeALaCarteTime int = 5
	// MenuTypeSpecial is the special (season / event) menu.
	MenuTypeSpecial int = 6
)

// menuTypeLegacyNames maps each canonical code to the legacy string it
// replaces. It is the only place where the numeric <-> name mapping lives.
var menuTypeLegacyNames = map[int]string{
	MenuTypeClosedConventional: "closed_conventional",
	MenuTypeClosedGroup:        "closed_group",
	MenuTypeALaCarte:           "a_la_carte",
	MenuTypeALaCarteGroup:      "a_la_carte_group",
	MenuTypeALaCarteTime:       "a_la_carte_time",
	MenuTypeSpecial:            "special",
}

// menuTypeLegacyAliases maps tolerated spellings (including the one added by
// the backoffice website builder) to their canonical code.
var menuTypeLegacyAliases = map[string]int{
	"":                    MenuTypeUnknown,
	"closed":              MenuTypeClosedConventional,
	"group":               MenuTypeClosedGroup,
	"a_la_carta":          MenuTypeALaCarte,
	"a_la_carta_grupo":    MenuTypeALaCarteGroup,
	"closed_conventional": MenuTypeClosedConventional,
	"closed_group":        MenuTypeClosedGroup,
	"a_la_carte":          MenuTypeALaCarte,
	"a_la_carte_group":    MenuTypeALaCarteGroup,
	"a_la_carte_time":     MenuTypeALaCarteTime,
	"special":             MenuTypeSpecial,
}

// MenuTypeFromName returns the canonical code of a legacy name, or
// MenuTypeUnknown when the name is not recognized.
func MenuTypeFromName(name string) int {
	return menuTypeLegacyAliases[strings.ToLower(strings.TrimSpace(name))]
}

// MenuTypeName returns the legacy name of a canonical code, or "" for an
// unknown code.
func MenuTypeName(code int) string {
	return menuTypeLegacyNames[code]
}

// MenuTypeFromAny is the canonical reader: it accepts a number (JSON or DB
// value), a legacy string or an empty value and returns the canonical code.
// Anything unrecognized resolves to MenuTypeUnknown so a bad row is never
// silently rewritten as the default.
// Use it instead of parsing menu_type by hand, both for the rolling deploy
// (number or legacy string on the wire) and for a not yet migrated DB.
func MenuTypeFromAny(raw any) int {
	switch v := raw.(type) {
	case nil:
		return MenuTypeUnknown
	case int:
		return normalizeMenuTypeCode(v)
	case int64:
		return normalizeMenuTypeCode(int(v))
	case int32:
		return normalizeMenuTypeCode(int(v))
	case uint64:
		return normalizeMenuTypeCode(int(v))
	case float64:
		return normalizeMenuTypeCode(int(v))
	case float32:
		return normalizeMenuTypeCode(int(v))
	case string:
		return menuTypeFromString(v)
	case []byte:
		return menuTypeFromString(string(v))
	case sql.NullInt64:
		if !v.Valid {
			return MenuTypeUnknown
		}
		return normalizeMenuTypeCode(int(v.Int64))
	case sql.NullInt32:
		if !v.Valid {
			return MenuTypeUnknown
		}
		return normalizeMenuTypeCode(int(v.Int32))
	case sql.NullString:
		return menuTypeFromString(v.String)
	default:
		// Anything else is read as text (e.g. a named type from a driver)
		// and resolved through the same path; it can never recurse because
		// anyToString always yields a string.
		return menuTypeFromString(strings.Trim(strings.TrimSpace(anyToString(raw)), `"`))
	}
}

// menuTypeFromString resolves a legacy string column value. Numbers stored as
// text (a VARCHAR column that already holds "3") are also accepted, so the
// helper works against a migrated and an unmigrated database alike.
func menuTypeFromString(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return MenuTypeUnknown
	}
	if code, err := strconv.Atoi(s); err == nil {
		return normalizeMenuTypeCode(code)
	}
	return MenuTypeFromName(s)
}

// normalizeMenuTypeCode keeps only the documented codes.
func normalizeMenuTypeCode(code int) int {
	if _, ok := menuTypeLegacyNames[code]; ok {
		return code
	}
	return MenuTypeUnknown
}

// menuTypeWriteCode is the code stored on write: an unusable input falls back
// to the closed conventional default so a menu never ends up untyped.
func menuTypeWriteCode(raw any) int {
	if code := MenuTypeFromAny(raw); code != MenuTypeUnknown {
		return code
	}
	return MenuTypeClosedConventional
}

// menuTypeWriteCodeOr is menuTypeWriteCode with an explicit fallback, used by
// the paths that keep the current value instead of defaulting.
func menuTypeWriteCodeOr(raw any, fallback int) int {
	if code := MenuTypeFromAny(raw); code != MenuTypeUnknown {
		return code
	}
	if _, ok := menuTypeLegacyNames[fallback]; ok {
		return fallback
	}
	return MenuTypeClosedConventional
}

// IsGroupMenuTypeCode reports whether a code is one of the two group menus
// (closed group / group carta).
func IsGroupMenuTypeCode(code int) bool {
	return code == MenuTypeClosedGroup || code == MenuTypeALaCarteGroup
}

// IsPublicMenuTypeCode reports whether a code belongs to a menu type that is
// published on the public site. Code 5 (carta by time) is not published.
func IsPublicMenuTypeCode(code int) bool {
	switch code {
	case MenuTypeClosedConventional, MenuTypeClosedGroup, MenuTypeALaCarte, MenuTypeALaCarteGroup, MenuTypeSpecial:
		return true
	default:
		return false
	}
}

// IsSpecialMenuType reports whether a raw value is the special menu type,
// accepting the numeric code and the legacy string alike.
func IsSpecialMenuType(raw any) bool {
	return MenuTypeFromAny(raw) == MenuTypeSpecial
}
