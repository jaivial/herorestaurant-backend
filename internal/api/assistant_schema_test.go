package api

import (
	"encoding/json"
	"testing"
)

func TestAssistantDefinitionsHaveValidSchemas(t *testing.T) {
	for _, d := range assistantToolDefs() {
		var v map[string]any
		if err := json.Unmarshal(d.InputSchema, &v); err != nil {
			t.Errorf("%s: %v", d.Name, err)
		}
		if v["type"] != "object" {
			t.Errorf("%s schema is not object", d.Name)
		}
	}
}
