package migrations

import (
	"strings"
	"testing"
)

func TestMenusSchemaCompatibilityMigrationTargetsRenamedTable(t *testing.T) {
	source, err := migrationFS.ReadFile("085_menus_schema_compatibility.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, column := range []string{"menu_type", "is_draft", "editor_version"} {
		if !strings.Contains(text, "TABLE_NAME = 'menus'") || !strings.Contains(text, "COLUMN_NAME = '"+column+"'") {
			t.Fatalf("compatibility migration must guard menus.%s", column)
		}
		if !strings.Contains(text, "ALTER TABLE `menus` ADD COLUMN `"+column+"`") {
			t.Fatalf("compatibility migration must add menus.%s", column)
		}
	}
}

func TestMenusSchemaCompatibilityMigrationRenamesLegacyTableWhenNeeded(t *testing.T) {
	source, err := migrationFS.ReadFile("085_menus_schema_compatibility.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "TABLE_NAME = 'menusDeGrupos'") ||
		!strings.Contains(text, "RENAME TABLE `menusDeGrupos` TO `menus`") {
		t.Fatal("compatibility migration must repair stale legacy table name")
	}
}
