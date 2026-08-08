package api

import (
	"context"
	"encoding/json"
	"testing"
)

// Registry invariants: every tool exposed to the model and every tool executed
// must come from the single assistantToolRegistry. These tests fail while the
// registry is missing and pin its contract as new tools are added.

func TestAssistantToolRegistryDefsComplete(t *testing.T) {
	defs := assistantToolDefs()
	if len(defs) == 0 {
		t.Fatal("no tool defs exposed")
	}
	byName := map[string]assistantToolDef{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	for _, tool := range assistantToolRegistry {
		d, ok := byName[tool.Name]
		if !ok {
			t.Errorf("registry tool %s missing from defs", tool.Name)
			continue
		}
		if d.Description != tool.Description {
			t.Errorf("def description drift for %s", tool.Name)
		}
		if string(d.InputSchema) != string(tool.Schema) {
			t.Errorf("def schema drift for %s", tool.Name)
		}
	}
	if len(byName) != len(assistantToolRegistry) {
		t.Errorf("defs/registry count mismatch: %d vs %d", len(byName), len(assistantToolRegistry))
	}
}

func TestAssistantToolRegistryUniqueNames(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range assistantToolRegistry {
		if seen[tool.Name] {
			t.Errorf("duplicate tool name %s", tool.Name)
		}
		seen[tool.Name] = true
	}
}

func TestAssistantToolRegistrySchemasValid(t *testing.T) {
	for _, tool := range assistantToolRegistry {
		var v map[string]any
		if err := json.Unmarshal(tool.Schema, &v); err != nil {
			t.Errorf("%s: schema invalid json: %v", tool.Name, err)
			continue
		}
		if v["type"] != "object" {
			t.Errorf("%s schema type is not object", tool.Name)
		}
	}
}

func TestAssistantToolRegistryWritesRequireConfirmation(t *testing.T) {
	for _, tool := range assistantToolRegistry {
		if tool.Write != assistantToolWrites(tool.Name) {
			t.Errorf("writes() inconsistent with registry for %s", tool.Name)
		}
		if tool.Write && !tool.Confirm {
			t.Errorf("write tool %s must require confirmation", tool.Name)
		}
		if tool.Confirm != assistantToolConfirmRequired(tool.Name) {
			t.Errorf("confirm required inconsistent for %s", tool.Name)
		}
	}
}

func TestAssistantToolRegistrySectionsValid(t *testing.T) {
	known := map[string]bool{
		"reservas": true, "menus": true, "comida": true, "stock": true, "pos": true,
		"ajustes": true, "miembros": true, "fichaje": true, "horarios": true,
		"facturas": true, "reportes": true, "estadisticas": true,
		"estado_cuenta": true, "website": true, "site-builder": true, "plataforma": true,
	}
	for _, tool := range assistantToolRegistry {
		if !known[tool.Section] {
			t.Errorf("tool %s has invalid section %q", tool.Name, tool.Section)
		}
	}
}

func TestAssistantToolRegistryHandlersSet(t *testing.T) {
	for _, tool := range assistantToolRegistry {
		if tool.Handler == nil {
			t.Errorf("tool %s has no handler", tool.Name)
		}
	}
}

func TestAssistantExecuteToolUnsafeDispatch(t *testing.T) {
	s := &Server{}
	if _, err := s.assistantExecuteToolUnsafe(context.Background(), 1, "does_not_exist", json.RawMessage(`{}`)); err == nil {
		t.Error("unknown tool must error")
	}
	if _, err := s.assistantExecuteToolUnsafe(context.Background(), 0, "restaurant_info", json.RawMessage(`{}`)); err == nil {
		t.Error("restaurantID<=0 must error before dispatch")
	}
}

func TestAssistantToolAllowedCoversEveryTool(t *testing.T) {
	admin := boAuth{Role: "admin", User: boUser{ID: 1}}
	for _, tool := range assistantToolRegistry {
		if !assistantToolAllowed(admin, tool.Name) {
			t.Errorf("admin denied tool %s (section %s)", tool.Name, tool.Section)
		}
	}
	jefe := boAuth{Role: "jefe_cocina", User: boUser{ID: 2}}
	for _, tool := range assistantToolRegistry {
		allowed := assistantToolAllowed(jefe, tool.Name)
		if tool.Section == "comida" {
			if !allowed {
				t.Errorf("jefe_cocina denied comida tool %s", tool.Name)
			}
		} else if allowed {
			t.Errorf("jefe_cocina unexpectedly allowed %s (section %s)", tool.Name, tool.Section)
		}
	}
	if assistantToolAllowed(admin, "not_a_tool") {
		t.Error("unknown tool must be denied")
	}
}

func TestAssistantAnonymousCannotCallBackofficeTools(t *testing.T) {
	s := &Server{}
	for _, tool := range assistantToolRegistry {
		if !tool.BackofficeOnly {
			continue
		}
		if _, err := s.assistantExecuteTool(context.Background(), 1, tool.Name, json.RawMessage(`{}`)); err == nil {
			t.Errorf("anonymous session must be denied backoffice-only tool %s", tool.Name)
		}
		if tool.Write && !tool.Confirm {
			t.Errorf("write tool %s must require confirmation", tool.Name)
		}
	}
	for _, tool := range assistantToolRegistry {
		if tool.BackofficeOnly {
			continue
		}
		if tool.Write {
			t.Errorf("write tool %s must also be backoffice-only", tool.Name)
		}
	}
}

func TestAssistantConfirmationHelpers(t *testing.T) {
	s := &Server{confirmationStore: newConfirmationStore()}
	args := json.RawMessage(`{"date":"2026-08-07","time":"20:00","people":2,"name":"Ana"}`)
	out, err := s.assistantRequireConfirmation(1, "create_booking", args)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	tok, _ := v["confirmation_token"].(string)
	if tok == "" {
		t.Fatalf("missing token: %s", out)
	}
	if err := s.assistantConsumeConfirmation(tok, 1, "create_booking", args); err != nil {
		t.Fatalf("consume failed: %v", err)
	}
	if err := s.assistantConsumeConfirmation(tok, 1, "create_booking", args); err == nil {
		t.Error("replay must be rejected")
	}
	if err := s.assistantConsumeConfirmation("", 1, "create_booking", args); err == nil {
		t.Error("missing token must be rejected")
	}
}

func TestAssistantRequireConfirmationCreatesStoreLazily(t *testing.T) {
	s := &Server{}
	out, err := s.assistantRequireConfirmation(1, "create_booking", json.RawMessage(`{"date":"2026-08-07","time":"20:00","people":2,"name":"Ana"}`))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v["confirmation_token"] == nil {
		t.Fatalf("missing token: %s", out)
	}
}
