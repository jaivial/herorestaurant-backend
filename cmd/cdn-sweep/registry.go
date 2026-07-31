package main

import (
	"context"
	"database/sql"
	"sort"
)

// referenceColumns is the reviewed registry of every column that can point at
// an object in the public CDN zone. It is deliberately explicit: the sweep
// treats "not in this set" as "orphaned", so a forgotten column would mean
// deleting live images.
var referenceColumns = []struct{ Table, Column string }{
	{"comida_items", "foto_url"},
	{"comida_items", "foto_path"},
	{"PLATOS", "foto_path"},
	{"BEBIDAS", "foto_path"},
	{"CAFES", "foto_path"},
	{"POSTRES", "foto_url"},
	{"VINOS", "foto_path"},
	{"VINOS", "ai_generated_img"},
	{"stock_items", "image_url"},
	{"stock_recipe_steps", "image_url"},
	{"stock_recipe_steps", "image_object_path"},
	{"group_menu_section_dishes_v2", "foto_path"},
	{"group_menu_section_dishes_v2", "ai_generated_img"},
	{"group_menu_section_dishes_v2", "ai_image_url"},
	{"menus", "menu_preview_image_path"},
	{"menus", "special_menu_image_url"},
	{"menu_slider_images", "image_path"},
	{"restaurant_branding", "logo_url"},
	{"restaurant_members", "photo_url"},
	{"restaurant_tables", "texture_image_url"},
	{"website_pages", "og_image"},
	{"site_builder_assets", "thumbnail_url"},
	{"site_builder_component_registry", "thumbnail_url"},
	{"website_templates", "thumbnail_url"},
	{"invoices", "account_image_url"},
}

// ignoredURLColumns hold outbound links, provider endpoints or documents that
// never live in the public image zone. They are listed explicitly so the
// unknown-column guard stays meaningful rather than being switched off.
var ignoredURLColumns = map[string]bool{
	"ai_image_providers.base_url":             true,
	"ai_image_providers.docs_url":             true,
	"conversation_messages.media_url":         true,
	"invoices.pdf_url":                        true,
	"restaurant_info.menu_url":                true,
	"restaurant_integrations.n8n_webhook_url": true,
	"restaurant_integrations.uazapi_url":      true,
	"restaurants.menu_url":                    true,
	"restaurants.website_url":                 true,
	"uazapi_servers.base_url":                 true,
}

func isIgnoredURLColumn(qualified string) bool {
	return ignoredURLColumns[qualified]
}

// unregisteredImageColumns reports discovered URL-ish columns that are neither
// registered as references nor explicitly ignored.
func unregisteredImageColumns(discovered []string, registered map[string]bool) []string {
	var unknown []string
	for _, column := range discovered {
		if registered[column] || isIgnoredURLColumn(column) {
			continue
		}
		unknown = append(unknown, column)
	}
	sort.Strings(unknown)
	return unknown
}

func registeredColumnSet() map[string]bool {
	out := map[string]bool{}
	for _, entry := range referenceColumns {
		out[entry.Table+"."+entry.Column] = true
	}
	return out
}

// discoverURLColumns finds every text column whose name suggests it stores an
// image or URL. It is the tripwire for the registry going stale.
func discoverURLColumns(ctx context.Context, database *sql.DB, schema string) ([]string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT CONCAT(TABLE_NAME,'.',COLUMN_NAME) FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA=?
		   AND DATA_TYPE IN ('varchar','text')
		   AND (COLUMN_NAME LIKE '%foto%' OR COLUMN_NAME LIKE '%image%'
		        OR COLUMN_NAME LIKE '%img%' OR COLUMN_NAME LIKE '%logo%'
		        OR COLUMN_NAME LIKE '%photo%' OR COLUMN_NAME LIKE '%\_url')
		 ORDER BY TABLE_NAME, COLUMN_NAME`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		out = append(out, column)
	}
	return out, rows.Err()
}

// collectReferenced reads every registered column. A column that does not exist
// in this schema is skipped; a query that fails is reported, because treating a
// failure as "no references" would mark live images as orphans.
func collectReferenced(ctx context.Context, database *sql.DB, schema string) (map[string]bool, error) {
	referenced := map[string]bool{}
	for _, entry := range referenceColumns {
		var exists int
		if err := database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.COLUMNS
			  WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND COLUMN_NAME=?`,
			schema, entry.Table, entry.Column).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			continue
		}
		// Table and column names come from the fixed registry above, never from
		// user input, so interpolation here cannot be abused.
		rows, err := database.QueryContext(ctx,
			`SELECT `+"`"+entry.Column+"`"+` FROM `+"`"+entry.Table+"`"+
				` WHERE `+"`"+entry.Column+"`"+` IS NOT NULL AND `+"`"+entry.Column+"`"+` <> ''`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var value sql.NullString
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return nil, err
			}
			if normalized := normalizeObjectPath(value.String); normalized != "" {
				referenced[normalized] = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return referenced, nil
}
