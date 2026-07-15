package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// botTenantConfig carries per-restaurant personalization knobs stored in
// whatsapp_bot_config.config_json.
type botTenantConfig struct {
	// Model overrides the global BotModel for this restaurant (e.g. MiniMax-M2).
	Model              string `json:"model"`
	LanguageDefault    string `json:"language_default"`
	Tone               string `json:"tone"`
	GreetingStyle      string `json:"greeting_style"`
	DisableAttachments bool   `json:"disable_attachments"`
	CustomInstructions string `json:"custom_instructions"`
	ContactPhone       string `json:"contact_phone"`
	// Rules overrides the default critical rules block of the system prompt.
	// Empty means use botDefaultRules.
	Rules string `json:"rules"`
}

func parseBotTenantConfig(raw string) botTenantConfig {
	cfg := botTenantConfig{LanguageDefault: "es", Tone: "cercano y profesional"}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg
	}
	_ = json.Unmarshal([]byte(raw), &cfg)
	if strings.TrimSpace(cfg.LanguageDefault) == "" {
		cfg.LanguageDefault = "es"
	}
	if strings.TrimSpace(cfg.Tone) == "" {
		cfg.Tone = "cercano y profesional"
	}
	return cfg
}

func botSchema(s string) json.RawMessage { return json.RawMessage(s) }

// botToolDefs returns the tool definitions offered to the LLM. Attachment
// tools are gated out when the tenant disables them.
func botToolDefs(cfg botTenantConfig) []botToolDef {
	defs := []botToolDef{
		{
			Name:        "send_message",
			Description: "Envía un mensaje de WhatsApp al cliente. ESTA ES LA HERRAMIENTA PRINCIPAL PARA COMUNICARTE. Siempre responde usando esta herramienta, nunca texto plano.",
			InputSchema: botSchema(`{"type":"object","properties":{"message":{"type":"string","description":"El mensaje de WhatsApp a enviar."}},"required":["message"]}`),
		},
		{
			Name:        "get_restaurant_info",
			Description: "Obtiene información del restaurante: nombre, teléfono, email, dirección y web.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_rice_menu",
			Description: "Obtiene los tipos de arroz activos en la carta del restaurante. ÚSALO SIEMPRE antes de hablar de arroces: nunca inventes tipos de arroz.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "list_menus",
			Description: "Lista los menús reservables activos con su categoría (menú cerrado convencional, menú cerrado de grupo, a la carta convencional, a la carta de grupo, menú especial), su precio y subtítulo. ÚSALO cuando el cliente pregunte por los menús disponibles.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_menu_details",
			Description: "Obtiene toda la información de UN menú por su menu_id: los platos de cada sección (título, descripción, precio, suplemento), el precio del menú y sus condiciones (bebida y precio por persona si la bebida es ilimitada, tamaño mínimo de grupo, máximo de platos principales por mesa, si incluye café y comentarios). Usa list_menus primero para conocer el menu_id.",
			InputSchema: botSchema(`{"type":"object","properties":{"menu_id":{"type":"integer","description":"ID del menú (de list_menus)"}},"required":["menu_id"]}`),
		},
		{
			Name:        "get_coffee_menu",
			Description: "Obtiene la carta de cafés del restaurante (nombre, precio, descripción).",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_drinks_menu",
			Description: "Obtiene la carta de bebidas y refrescos del restaurante (nombre, precio, descripción).",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_wines_menu",
			Description: "Obtiene la carta de vinos del restaurante agrupados por tipo (tinto, blanco, cava...), con bodega, denominación de origen, año y precio.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_default_schedule",
			Description: "Obtiene el horario POR DEFECTO del restaurante: qué días de la semana abre, los turnos de mediodía y noche por defecto y el límite diario. ÚSALO para responder sobre el horario general o qué días abre.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_day_schedule",
			Description: "Obtiene el horario REAL de una fecha concreta: si ese día de la semana abre por defecto y si existe una configuración especial que SOBREESCRIBE el horario general para ese día. Devuelve las horas efectivas y si el restaurante abre ese día. ÚSALO SIEMPRE antes de aceptar una fecha de reserva.",
			InputSchema: botSchema(`{"type":"object","properties":{"date":{"type":"string","description":"Fecha en formato dd/MM/yyyy o YYYY-MM-DD"}},"required":["date"]}`),
		},
		{
			Name:        "check_day_capacity",
			Description: "Verifica si un día tiene disponibilidad general: límite diario, personas ya reservadas y plazas libres.",
			InputSchema: botSchema(`{"type":"object","properties":{"date":{"type":"string","description":"Fecha en formato dd/MM/yyyy o YYYY-MM-DD"}},"required":["date"]}`),
		},
		{
			Name:        "check_availability_for_party",
			Description: "Verifica si un número de personas cabe en una fecha concreta.",
			InputSchema: botSchema(`{"type":"object","properties":{"date":{"type":"string","description":"Fecha en formato dd/MM/yyyy o YYYY-MM-DD"},"party_size":{"type":"integer","description":"Número de personas"}},"required":["date","party_size"]}`),
		},
		{
			Name:        "get_bookings",
			Description: "Obtiene las reservas futuras activas del cliente actual (por su teléfono).",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "create_booking",
			Description: "Crea una nueva reserva. Requiere confirmed=true como confirmación de seguridad tras repetir los datos al cliente.",
			InputSchema: botSchema(`{"type":"object","properties":{
				"date":{"type":"string","description":"Fecha YYYY-MM-DD"},
				"time":{"type":"string","description":"Hora HH:MM"},
				"people":{"type":"integer","description":"Número de personas"},
				"name":{"type":"string","description":"Nombre del cliente"},
				"rice_type":{"type":"string","description":"Tipo de arroz (opcional)"},
				"rice_servings":{"type":"integer","description":"Raciones de arroz, mínimo 2 (opcional)"},
				"high_chairs":{"type":"integer","description":"Tronas (opcional)"},
				"baby_strollers":{"type":"integer","description":"Carritos de bebé (opcional)"},
				"commentary":{"type":"string","description":"Comentarios (opcional)"},
				"confirmed":{"type":"boolean","description":"DEBE ser true para ejecutar la reserva"}
			},"required":["date","time","people","confirmed"]}`),
		},
		{
			Name:        "cancel_booking",
			Description: "Cancela una reserva existente del cliente. Requiere confirmed=true.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer","description":"ID de la reserva"},"confirmed":{"type":"boolean","description":"DEBE ser true para cancelar"}},"required":["booking_id","confirmed"]}`),
		},
		{
			Name:        "modify_booking",
			Description: "Modifica una reserva existente (fecha, hora, personas, arroz, tronas, carritos). Requiere confirmed=true.",
			InputSchema: botSchema(`{"type":"object","properties":{
				"booking_id":{"type":"integer","description":"ID de la reserva"},
				"date":{"type":"string","description":"Nueva fecha YYYY-MM-DD (opcional)"},
				"time":{"type":"string","description":"Nueva hora HH:MM (opcional)"},
				"people":{"type":"integer","description":"Nuevo número de personas (opcional)"},
				"rice_type":{"type":"string","description":"Nuevo tipo de arroz (opcional)"},
				"rice_servings":{"type":"integer","description":"Nuevas raciones (opcional)"},
				"clear_rice":{"type":"boolean","description":"true para quitar el arroz (opcional)"},
				"high_chairs":{"type":"integer","description":"Tronas (opcional)"},
				"baby_strollers":{"type":"integer","description":"Carritos (opcional)"},
				"confirmed":{"type":"boolean","description":"DEBE ser true para ejecutar"}
			},"required":["booking_id","confirmed"]}`),
		},
		{
			Name:        "send_menu_buttons",
			Description: "Envía un mensaje con botones de respuesta rápida al cliente (máximo 3 opciones).",
			InputSchema: botSchema(`{"type":"object","properties":{"text":{"type":"string","description":"Texto del mensaje"},"choices":{"type":"array","items":{"type":"string"},"description":"Opciones de los botones"}},"required":["text","choices"]}`),
		},
	}

	if !cfg.DisableAttachments {
		defs = append(defs,
			botToolDef{
				Name:        "send_image",
				Description: "Envía una imagen al cliente por WhatsApp desde una URL pública (fotos de platos, carta, local).",
				InputSchema: botSchema(`{"type":"object","properties":{"url":{"type":"string","description":"URL pública de la imagen"},"caption":{"type":"string","description":"Pie de foto (opcional)"}},"required":["url"]}`),
			},
			botToolDef{
				Name:        "send_document",
				Description: "Envía un documento (por ejemplo la carta en PDF) al cliente desde una URL pública.",
				InputSchema: botSchema(`{"type":"object","properties":{"url":{"type":"string","description":"URL pública del documento"},"filename":{"type":"string","description":"Nombre de archivo (opcional)"},"caption":{"type":"string","description":"Texto acompañante (opcional)"}},"required":["url"]}`),
			},
			botToolDef{
				Name:        "send_location",
				Description: "Envía la ubicación del restaurante al cliente (dirección y coordenadas si están disponibles).",
				InputSchema: botSchema(`{"type":"object","properties":{}}`),
			},
			botToolDef{
				Name:        "send_contact",
				Description: "Envía una tarjeta de contacto del restaurante para que el cliente pueda llamar (escalada a humano).",
				InputSchema: botSchema(`{"type":"object","properties":{}}`),
			},
		)
	}

	return defs
}

// parseBotDate accepts dd/MM/yyyy or YYYY-MM-DD and returns ISO date.
func parseBotDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", "02/01/2006", "2/1/2006"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("fecha inválida: %q (usa dd/MM/yyyy o YYYY-MM-DD)", raw)
}
