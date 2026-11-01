package main

// Coordination id: wa_bot_menus_full_crud_v1
//
// Exact request contracts of every write in the menus section (carta: platos,
// cafés, bebidas, vinos, postres, categorías; menú del día/finde; menús de
// grupo legacy y v2: secciones, platos, visibilidad, slider, reserva en el
// día). admin_describe returns them so Forky and the WhatsApp bot can build
// panel_change / tool_chain pipelines without guessing bodies.

const carteItemFields = `{nombre:string (obligatorio al crear), precio:number €, descripcion:string, titulo:string (grupo dentro de la carta), suplemento:number, active:boolean (NO activo ni string), alergenos:[string], tipo:string, categoria:string (por nombre; se crea si no existe en platos)}. En PATCH todos opcionales: envía solo lo que cambia.`

const vinoFields = `{tipo:"TINTO"|"BLANCO"|"ROSADO"|"CAVA"|… (obligatorio al crear), nombre (obligatorio), bodega (obligatorio), precio:number>0 (obligatorio), descripcion, denominacion_origen, graduacion:number, anyo:string, active:boolean}. En PATCH todos opcionales.`

const menuDishFields = `{descripcion:string (nombre del plato, obligatorio al crear), tipo:"ENTRANTE"|"PRINCIPAL"|"ARROZ" (obligatorio al crear), alergenos:[string], active:boolean}. En PATCH todos opcionales.`

const sectionDishFields = `{id?:id del plato existente (omitido = nuevo), catalog_dish_id?:id del catálogo (admin_get_dishes_catalog_search), title (obligatorio), description, description_enabled:boolean, allergens:[string], supplement_enabled:boolean, supplement_price:number|null, price:number|null (solo a la carta), active:boolean, apply_to_catalog:boolean}`

const legacyGroupMenuFields = `{menu_title (obligatorio), menu_subtitle, price:number>0 (obligatorio), active:boolean, min_party_size:int, main_dishes_limit:boolean, main_dishes_limit_number:int, included_coffee:boolean, entrantes:[string], principales:{titulo_principales:string, items:[string]}, postre:[string], beverage:{type, price_per_person, has_supplement, supplement_price}, comments:[string]}`

var menusBodiesEs = map[string]string{
	// Carta por tipo (alias de /comida/{tipo}).
	"POST /platos":                `Crea un plato de la carta. ` + carteItemFields,
	"PATCH /platos/{id}":          `Edita un plato de la carta. ` + carteItemFields,
	"DELETE /platos/{id}":         `Sin body. Elimina el plato.`,
	"POST /platos/{id}/toggle":    `Sin body: alterna activo/inactivo (para fijar un valor usa PATCH con active).`,
	"POST /bebidas":               `Crea una bebida. ` + carteItemFields,
	"PATCH /bebidas/{id}":         `Edita una bebida. ` + carteItemFields,
	"DELETE /bebidas/{id}":        `Sin body. Elimina la bebida.`,
	"POST /bebidas/{id}/toggle":   `Sin body: alterna activo/inactivo (para fijar un valor usa PATCH con active).`,
	"POST /cafes/{id}/toggle":     `Sin body: alterna activo/inactivo (para fijar un valor usa PATCH con active).`,
	"POST /postres":               `Crea un postre de la carta general: {descripcion:string (nombre, obligatorio), alergenos:[string], active:boolean}.`,
	"PATCH /postres/{id}":         `Edita un postre: {descripcion?, alergenos?:[string], active?:boolean}.`,
	"DELETE /postres/{id}":        `Sin body. Elimina el postre.`,
	"POST /vinos":                 `Crea un vino. ` + vinoFields,
	"PATCH /vinos/{id}":           `Edita un vino. ` + vinoFields,
	"DELETE /vinos/{id}":          `Sin body. Elimina el vino.`,
	"POST /dishes-catalog/upsert": `Crea (sin id) o edita (con id) un plato del catálogo compartido de los menús v2: {id?, title (obligatorio), description, allergens:[string], default_supplement_enabled:boolean, default_supplement_price:number|null}.`,
	// Categorías.
	"POST /comida/categorias":         `{name (obligatorio, ≤120), foodType:"platos"|"bebidas"|"vinos"|"cafes"|"postres" o vacío/"global", global?:boolean, active?:boolean}. Rechaza nombres que ya existen en un ámbito solapado.`,
	"PATCH /comida/categorias/{id}":   `{name?, foodType?, global?, active?:boolean}. Solo categorías editables (id>0 de admin_get_comida_categorias).`,
	"DELETE /comida/categorias/{id}":  `Sin body. Solo categorías editables.`,
	"POST /comida/platos/categorias":  `Categoría legacy de platos: {name (obligatorio), label?, slug?}.`,
	"POST /comida/bebidas/categorias": `Categoría legacy de bebidas: {name (obligatorio), label?, slug?}.`,
	// Menú del día / fin de semana (tablas DIA y FINDE).
	"POST /menus/dia/dishes":             `Añade un plato al menú del día. ` + menuDishFields,
	"PATCH /menus/dia/dishes/{id}":       `Edita un plato del menú del día. ` + menuDishFields,
	"DELETE /menus/dia/dishes/{id}":      `Sin body. Quita el plato del menú del día.`,
	"POST /menus/dia/price":              `{price:"12.50" (string con el precio del menú del día)}.`,
	"POST /menus/finde/dishes":           `Añade un plato al menú de fin de semana. ` + menuDishFields,
	"PATCH /menus/finde/dishes/{id}":     `Edita un plato del menú de fin de semana. ` + menuDishFields,
	"DELETE /menus/finde/dishes/{id}":    `Sin body. Quita el plato del menú de fin de semana.`,
	"POST /menus/finde/price":            `{price:"25.00" (string con el precio del menú de fin de semana)}.`,
	"POST /menu-visibility":              `{menuKey:"menudeldia"|"menufindesemana", isActive:boolean}.`,
	"PATCH /restaurant/pages/visibility": `Solo lo que cambia: {cafe_page_active, bebidas_page_active, postres_page_active, vinos_page_active: boolean; cafes_web_placement, bebidas_web_placement, postres_web_placement, vinos_web_placement: "inside_menus"|"independent_section"}.`,
	// Menús de grupo legacy (/app/menus antiguo).
	"POST /group-menus":             `Crea un menú de grupo legacy: ` + legacyGroupMenuFields,
	"PUT /group-menus/{id}":         `REEMPLAZA el menú de grupo legacy completo (copia antes admin_get_group_menus_id): ` + legacyGroupMenuFields,
	"DELETE /group-menus/{id}":      `Sin body.`,
	"POST /group-menus/{id}/toggle": `Sin body: alterna activo/inactivo.`,
	// Menús v2: secciones y platos (todos los tipos de menú).
	"PUT /group-menus-v2/{id}/sections":                               `REEMPLAZA la lista de secciones (las omitidas se BORRAN con sus platos; copia las actuales de admin_get_group_menus_v2_id): {sections:[{id?:id existente (omitido = nueva), title (obligatorio), display_title?, subtitle?, tab_label?, kind:"entrantes"|"principales"|"postres"|"arroces"|"bebidas"|"custom", dessert_source?:"general"|"custom" (solo postres), position:int, annotations?:[string]}]}. Devuelve sections con sus id.`,
	"DELETE /group-menus-v2/{id}/sections/{sectionId}":                `Sin body. Borra la sección y sus platos.`,
	"PUT /group-menus-v2/{id}/sections/{sectionId}/dishes":            `REEMPLAZA los platos de la sección (los omitidos se BORRAN; copia antes admin_get_group_menus_v2_id_sections_sectionid_dishes): {dishes:[` + sectionDishFields + `]}. Devuelve dishes con sus id.`,
	"PATCH /group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}": `Edita UN plato sin tocar el resto: solo los campos que cambian de ` + sectionDishFields + `.`,
	"PATCH /group-menus-v2/{id}/sections/{sectionId}/annotations":     `{annotations:[string]} (notas bajo la sección; lista completa).`,
	"PATCH /group-menus-v2/{id}/sections/{sectionId}/dessert-source":  `{dessert_source:"general"|"custom"} (solo secciones de postres; general = espejo de la carta de postres).`,
	"PATCH /group-menus-v2/{id}/sections/{sectionId}/visibility":      `Solo lo que cambia: {public_page_active?:boolean, web_placement?:"inside_menus"|"independent_section"}.`,
	"POST /group-menus-v2/{id}/same-day-booking/{dishId}":             `Sin body: el plato (dishId de la sección) se puede reservar el mismo día.`,
	"DELETE /group-menus-v2/{id}/same-day-booking/{dishId}":           `Sin body: quita la reserva en el día del plato.`,
	"PATCH /group-menus-v2/{id}/slider":                               `{mode:"default"|"custom"|"both"|"hidden"}.`,
	"PUT /group-menus-v2/{id}/slider/images":                          `{image_ids:[id en el orden nuevo]} (ids de admin_get_group_menus_v2_id_slider).`,
	"DELETE /group-menus-v2/{id}/slider/images/{imageId}":             `Sin body.`,
	"POST /group-menus-v2/postres/resolve":                            `Sin body: devuelve (y crea si falta) el menú de la carta general de postres.`,
}

func init() {
	for k, v := range menusBodiesEs {
		bodiesEs[k] = v
	}
}
