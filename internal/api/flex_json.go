package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Coordination id: wa_bot_flexible_types_v1
//
// The panel sends real JSON booleans and numbers, but model-built bodies
// (WhatsApp bot, Forky) often quote them: "active":"true", "precio":"0".
// The strict decoders rejected the whole body as "Invalid JSON" (prod
// 2026-11-01 14:57: six café inserts failed this way). These types accept
// both spellings, so the same request is valid whoever builds it.

// flexBool decodes true/false, "true"/"false", "1"/"0", 1/0, "si"/"no".
type flexBool bool

func (b *flexBool) UnmarshalJSON(raw []byte) error {
	v := strings.ToLower(strings.Trim(strings.TrimSpace(string(raw)), `"`))
	switch v {
	case "true", "1", "si", "sí", "yes", "on":
		*b = true
	case "false", "0", "no", "off", "":
		*b = false
	default:
		return fmt.Errorf("valor booleano no válido: %s", raw)
	}
	return nil
}

// flexFloat decodes numbers and numeric strings ("12.5", "12,5", "12 €").
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] != '"' {
		var n float64
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
		*f = flexFloat(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "€"))
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("número no válido: %q", s)
	}
	*f = flexFloat(n)
	return nil
}

// flexInt decodes integers and integer strings.
type flexInt int

func (i *flexInt) UnmarshalJSON(raw []byte) error {
	var f flexFloat
	if err := f.UnmarshalJSON(raw); err != nil {
		return err
	}
	*i = flexInt(int(f))
	return nil
}

func flexBoolPtr(p *flexBool) *bool {
	if p == nil {
		return nil
	}
	v := bool(*p)
	return &v
}

func flexFloatPtr(p *flexFloat) *float64 {
	if p == nil {
		return nil
	}
	v := float64(*p)
	return &v
}

func flexIntPtr(p *flexInt) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

// UnmarshalJSON lets the carta write bodies (platos, bebidas, cafés, vinos,
// postres) take quoted booleans and numbers, keeping the strict unknown-field
// check of readJSONBody: an alias decoder with DisallowUnknownFields runs
// first, then the flexible fields are converted to the typed ones.
func (r *comidaUpsertRequest) UnmarshalJSON(raw []byte) error {
	type plain comidaUpsertRequest
	var aux struct {
		plain
		Precio       *flexFloat `json:"precio,omitempty"`
		Suplemento   *flexFloat `json:"suplemento,omitempty"`
		Active       *flexBool  `json:"active,omitempty"`
		CategoryID   *flexInt   `json:"category_id,omitempty"`
		Graduacion   *flexFloat `json:"graduacion,omitempty"`
		AiGenerating *flexBool  `json:"ai_generating,omitempty"`
		DescEnabled  *flexBool  `json:"description_enabled,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	*r = comidaUpsertRequest(aux.plain)
	r.Precio, r.Suplemento, r.Graduacion = flexFloatPtr(aux.Precio), flexFloatPtr(aux.Suplemento), flexFloatPtr(aux.Graduacion)
	r.Active, r.AiGenerating = flexBoolPtr(aux.Active), flexBoolPtr(aux.AiGenerating)
	r.DescriptionEnabled = flexBoolPtr(aux.DescEnabled)
	r.CategoryID = flexIntPtr(aux.CategoryID)
	return nil
}

func (r *boMenuDishCreateRequest) UnmarshalJSON(raw []byte) error {
	type plain boMenuDishCreateRequest
	var aux struct {
		plain
		Active *flexBool `json:"active,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	*r = boMenuDishCreateRequest(aux.plain)
	r.Active = flexBoolPtr(aux.Active)
	return nil
}

func (r *boMenuDishPatchRequest) UnmarshalJSON(raw []byte) error {
	type plain boMenuDishPatchRequest
	var aux struct {
		plain
		Active *flexBool `json:"active,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	*r = boMenuDishPatchRequest(aux.plain)
	r.Active = flexBoolPtr(aux.Active)
	return nil
}

func (r *boPostreCreateRequest) UnmarshalJSON(raw []byte) error {
	type plain boPostreCreateRequest
	var aux struct {
		plain
		Active *flexBool `json:"active,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	*r = boPostreCreateRequest(aux.plain)
	r.Active = flexBoolPtr(aux.Active)
	return nil
}

func (r *boPostrePatchRequest) UnmarshalJSON(raw []byte) error {
	type plain boPostrePatchRequest
	var aux struct {
		plain
		Active *flexBool `json:"active,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	*r = boPostrePatchRequest(aux.plain)
	r.Active = flexBoolPtr(aux.Active)
	return nil
}

func (r *boVinoUpsertRequest) UnmarshalJSON(raw []byte) error {
	type plain boVinoUpsertRequest
	var aux struct {
		plain
		Precio     *flexFloat `json:"precio,omitempty"`
		Graduacion *flexFloat `json:"graduacion,omitempty"`
		Active     *flexBool  `json:"active,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	*r = boVinoUpsertRequest(aux.plain)
	r.Precio, r.Graduacion = flexFloatPtr(aux.Precio), flexFloatPtr(aux.Graduacion)
	r.Active = flexBoolPtr(aux.Active)
	return nil
}
