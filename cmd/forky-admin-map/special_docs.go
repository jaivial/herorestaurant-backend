package main

// Coordination id: forky_bot_special_full_v1
//
// Special menus (menu_type "special") and special dates (fechas festivas) are
// operated end to end by Forky (admin_call) and the WhatsApp bot
// (panel_change) through this map. Without the request contract the models
// guessed the bodies: 2026-10-27 the bot sent adelanto_amount to the catalogue
// section PATCH (which ignores it) and exhausted the turn. Each entry below is
// the exact contract of the handler, so admin_describe answers it.

// specialDateFields is the shared field list of the special-date save bodies.
const specialDateFields = `{date:"YYYY-MM-DD" (obligatorio), is_active:bool, title, description, custom_notice (aviso del paso 2 de la reserva web), ` +
	`prereserva_enabled:bool, prereserva_starts_on/prereserva_ends_on:"YYYY-MM-DD"|null, max_per_table_enabled:bool, max_per_table:int>=1 (obligatorio si max_per_table_enabled), ` +
	`mobility_enabled:bool, allow_customer_modification:bool, ask_adults/ask_high_chairs/ask_baby_strollers:bool, ` +
	`requires_adelanto:bool, adelanto_payment_methods:["card","bizum","transferencia","efectivo"] o ["stripe"] (stripe va solo), ` +
	`adelanto_unified:bool, adelanto_unified_amount:€/persona|null (si adelanto_unified=true manda sobre los de menú y sección), ` +
	`menus:[{menu_id:id de un menú activo del catálogo (admin_get_group_menus_v2) | custom_title+custom_image_url (menú personalizado; los dos obligatorios, nunca junto a menu_id), ` +
	`adelanto_amount:€/persona, price:€ (opcional, sustituye al del catálogo), position:int, completo:bool (marca el menú COMPLETO), ` +
	`sections:[{section_id (o id), adelanto_amount:€/persona|null, online_enabled:bool}] (solo menús especiales: adelanto y venta online POR SECCIÓN)}]}`

// specialBookingBlock is the `special` block of the booking bodies.
const specialBookingBlock = `special:{menus:[{special_date_menu_id, count, adelanto_payment_method?, items?:[dish_id], ` +
	`sections?:[{section_id, count, items?:[dish_id]}] (obligatorio en menús especiales por secciones; suma de count = count del menú)}], ` +
	`payment_method?, adelantos_paid?:[{method, amount}] (abonos ya recibidos)}. ` +
	`Los ids salen de admin_get_config_special_dates?date=… (menus[].id = special_date_menu_id, menus[].sections[].id = section_id); la suma de count = party_size.`

func init() {
	for k, v := range specialDescriptionsEs {
		descriptionsEs[k] = v
	}
}

// specialDescriptionsEs: short Spanish descriptions (catalog search, <200 bytes).
var specialDescriptionsEs = map[string]string{
	// Fechas especiales (festivas).
	"GET /config/special-dates":         "Fechas especiales (festivas: Navidad, Nochevieja, Reyes…): sin date lista todas; con date=YYYY-MM-DD da su configuración completa (menús, secciones, adelantos).",
	"GET /config/special-dates/stats":   "Estadísticas de una fecha especial (festiva): reservas, comensales y adelantos por menú y sección.",
	"POST /config/special-dates":        "REEMPLAZA entera una fecha especial (festiva), menús incluidos. Para cambiar solo algunos campos usa admin_patch_config_special_dates.",
	"PATCH /config/special-dates":       "Crea o cambia SOLO los campos enviados de una fecha especial (festiva): título, activa, pre-reserva, adelanto, métodos de pago, menús…",
	"POST /group-menus-v2/drafts":       "Crea un borrador de menú (menú especial: menu_type=special) y devuelve su menu_id.",
	"PATCH /group-menus-v2/{id}/basics": "Cambia los datos básicos de un menú (título, subtítulo, precio, activo, borrador, comensales mínimos, bebida…).",
	// Menús especiales.
	"PATCH /group-menus-v2/{id}/menu-type":                                          "Cambia el tipo de un menú (por ejemplo a menú especial: menu_type=special).",
	"POST /group-menus-v2/{id}/publish":                                             "Publica un menú (deja de ser borrador).",
	"POST /group-menus-v2/{id}/toggle-active":                                       "Activa o desactiva un menú (alterna su estado).",
	"DELETE /group-menus-v2/{id}":                                                   "Elimina un menú.",
	"GET /group-menus-v2/{id}":                                                      "Devuelve un menú completo (en menús especiales: imagen, secciones, CTA, visibilidad).",
	"GET /group-menus-v2":                                                           "Lista los menús de grupo y menús especiales (menu_type=special).",
	"PATCH /group-menus-v2/{id}/seo":                                                "Cambia el SEO público de un menú (slug, title, description y H1 en español e inglés).",
	"GET /group-menus-v2/{id}/special-sections":                                     "Lista las secciones de un menú especial (imagen, título y precio de cada una).",
	"POST /group-menus-v2/{id}/special-sections":                                    "Crea una sección en un menú especial.",
	"PATCH /group-menus-v2/{id}/special-sections/{sectionId}":                       "Cambia título o precio de una sección de un menú especial (el adelanto NO: va en la fecha especial).",
	"DELETE /group-menus-v2/{id}/special-sections/{sectionId}":                      "Elimina una sección de un menú especial.",
	"PUT /group-menus-v2/{id}/special-sections/order":                               "Reordena las secciones de un menú especial.",
	"PATCH /group-menus-v2/{id}/visibility":                                         "Visibilidad web de un menú especial: ubicación, publicado y fecha especial vinculada.",
	"PUT /group-menus-v2/{id}/special-cta":                                          "Botón de acción (CTA) de un menú especial en la web: reservar, WhatsApp o abrir otro menú.",
	"PUT /group-menus-v2/{id}/special-principales":                                  "Activa o desactiva la elección de principales por sección en un menú especial.",
	"POST /group-menus-v2/{id}/special-sections/{sectionId}/principales":            "Añade un plato principal elegible a una sección de un menú especial.",
	"DELETE /group-menus-v2/{id}/special-sections/{sectionId}/principales/{dishId}": "Quita un plato principal de una sección de un menú especial.",
	"PUT /group-menus-v2/{id}/special-group-booking":                                "Reserva de grupo de un menú especial: menú de grupo habilitado y principales obligatorios.",
	// Reservas (incluidas las de fecha especial).
	"POST /bookings":       "Crea una reserva desde el panel (normal o de fecha especial con su bloque special).",
	"PATCH /bookings/{id}": "Modifica una reserva (solo los campos enviados); special sustituye el bloque de fecha especial y null lo quita.",
}

const comidaCatalogWriteFields = `{nombre:string (obligatorio), precio:number €, descripcion:string, titulo:string, suplemento:number, active:boolean (NO activo), alergenos:[string], tipo:string, categoria:string}. En PATCH todos son opcionales; envía solo lo que cambia. category_id NO se usa para cafes/bebidas: usa categoria por nombre si hace falta.`

// bodiesEs: the exact request contract per "METHOD /path" (admin_describe).
var bodiesEs = map[string]string{
	"POST /comida/{tipo}":             `Crea un elemento del tipo de la ruta (platos|bebidas|cafes|vinos|postres). ` + comidaCatalogWriteFields + ` Vinos exige tipo, nombre, bodega y precio>0; postres acepta descripcion como nombre.`,
	"PATCH /comida/{tipo}/{id}":       `Edita un elemento del tipo de la ruta (platos|bebidas|cafes|vinos|postres). ` + comidaCatalogWriteFields,
	"DELETE /comida/{tipo}/{id}":      `Sin body. Elimina el elemento id solo si pertenece al tipo y restaurante actuales.`,
	"POST /cafes":                     `Crea un café en la carta especializada de cafés (comida_items.source_type=cafes). ` + comidaCatalogWriteFields,
	"PATCH /cafes/{id}":               `Edita un café de la carta especializada. ` + comidaCatalogWriteFields,
	"DELETE /cafes/{id}":              `Sin body. Elimina el café id del restaurante actual.`,
	"GET /config/special-dates":       `Sin body. query: date=YYYY-MM-DD opcional (sin date = lista de todas las fechas especiales con su id).`,
	"GET /config/special-dates/stats": `Sin body. query: date=YYYY-MM-DD (obligatorio).`,
	"POST /config/special-dates":      `REEMPLAZO TOTAL (lo que falte vuelve al valor por defecto y los menús se reescriben): ` + specialDateFields,
	"PATCH /config/special-dates": `FUSIÓN: envía date y SOLO lo que cambia; el resto se conserva. Campos: ` + specialDateFields +
		`. menus, si se envía, sustituye la lista ENTERA: copia la de admin_get_config_special_dates?date=… (vale tal cual) y modifícala. ` +
		`Las reservas ya hechas conservan el adelanto con el que se hicieron.`,
	"POST /group-menus-v2/drafts":                                                   `{menu_type?:"special"|"closed_conventional"|"closed_group"|"a_la_carte"|"a_la_carte_group"}. Devuelve menu_id. Luego: basics, special-sections, imagen y publish.`,
	"PATCH /group-menus-v2/{id}/basics":                                             `Solo los campos que cambian: {menu_title, menu_subtitle, price:€, active:bool, is_draft:bool, min_party_size:int, comments:[string], important_info:[string], beverage:{type, price_per_person, has_supplement, supplement_price}, included_coffee:bool, main_dishes_limit:bool, main_dishes_limit_number:int, show_dish_images:bool, show_section_tabs:bool, show_menu_preview_image:bool}. En un menú especial por secciones el precio que se reserva es el de cada sección.`,
	"PATCH /group-menus-v2/{id}/menu-type":                                          `{menu_type:"special"|"closed_conventional"|"closed_group"|"a_la_carte"|"a_la_carte_group"}`,
	"POST /group-menus-v2/{id}/publish":                                             `Sin body.`,
	"POST /group-menus-v2/{id}/toggle-active":                                       `Sin body: alterna activo/inactivo (lee antes el estado con admin_get_group_menus_v2_id).`,
	"DELETE /group-menus-v2/{id}":                                                   `Sin body.`,
	"PATCH /group-menus-v2/{id}/seo":                                                `{seo_slug, seo_title (≤180), seo_title_english, seo_description (≤500), seo_description_english, seo_h1 (≤180), seo_h1_english, seo_intro (≤4000), seo_intro_english, seo_faq (≤8000, bloques 'pregunta\nrespuesta' separados por línea en blanco), seo_faq_english}: strings. Se guardan TODOS: el campo que no envíes queda vacío, así que copia los actuales de admin_get_group_menus_v2_id.`,
	"POST /group-menus-v2/{id}/special-sections":                                    `{title:string}. La imagen de la sección se sube aparte (WhatsApp: panel_change media_upload kind=special_section; Forky: special_media_upload).`,
	"PATCH /group-menus-v2/{id}/special-sections/{sectionId}":                       `{title?:string, price?:€|null}. El ADELANTO de la sección NO va aquí: es por fecha especial (admin_patch_config_special_dates, menus[].sections[].adelanto_amount).`,
	"DELETE /group-menus-v2/{id}/special-sections/{sectionId}":                      `Sin body.`,
	"PUT /group-menus-v2/{id}/special-sections/order":                               `{ids:[section_id en el orden nuevo]}`,
	"DELETE /group-menus-v2/{id}/special-sections/{sectionId}/image":                `Sin body.`,
	"PATCH /group-menus-v2/{id}/visibility":                                         `Solo lo que cambia: {web_placement?:"inside_menus"|"independent_section", menu_public_active?:bool, special_date_id?:id de la fecha especial (admin_get_config_special_dates; 0 desvincula)}.`,
	"PUT /group-menus-v2/{id}/special-cta":                                          `Objeto completo (reemplaza): {enabled:bool, label:string, action:"reservas"|"whatsapp"|"menu", special_date_id (action=reservas: fecha especial a la que lleva), whatsapp_phone + whatsapp_message (action=whatsapp), menu_id (action=menu)}.`,
	"PUT /group-menus-v2/{id}/special-principales":                                  `{enabled:bool}`,
	"POST /group-menus-v2/{id}/special-sections/{sectionId}/principales":            `{dish_id: id de un plato de la carta (admin_get_platos)}`,
	"DELETE /group-menus-v2/{id}/special-sections/{sectionId}/principales/{dishId}": `Sin body.`,
	"PUT /group-menus-v2/{id}/special-group-booking":                                `{group_menu_enabled:bool, principales_required:bool} (principales_required=true fuerza group_menu_enabled=true).`,
	"POST /bookings": `{reservation_date:"YYYY-MM-DD", reservation_time:"HH:MM", party_size:int, customer_name, contact_phone, contact_phone_country_code:"34", contact_email?, commentary?, table_number?, preferred_floor_number?, children?, babyStrollers?, highChairs?, has_mobility_issues?, mobility_people?, is_event?, extras?:[id], ` +
		specialBookingBlock + `}`,
	"PATCH /bookings/{id}": `Solo los campos que cambian (mismos nombres que POST /bookings). ` + specialBookingBlock + ` En PATCH special sustituye el bloque entero y special:null lo quita.`,
}
