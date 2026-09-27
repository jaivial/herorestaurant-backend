package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_rag_fts_v1
//
// Restaurant knowledge (policies, FAQ, edge cases) lives as short chunks in
// restaurant_bot_ai_config.knowledge_json (MySQL, source of truth) and is
// indexed into an SQLite FTS5 table next to the conversation store. Each turn
// retrieves only the chunks relevant to the customer's message and the
// pipeline route, instead of pasting every rule into the system prompt.
//
// Why FTS5/BM25 instead of an embedding vector DB: the corpus per tenant is a
// few dozen short Spanish chunks, the vocabulary is domain-specific (arroz,
// menú, tronas...), and exact keyword + route tag matching is deterministic,
// needs no embedding model call on the hot path and is trivially debuggable.
// A tag column carries the pipeline route so a route always gets its rules.

type botKnowledgeChunk struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Tags are pipeline routes/intents that always pull this chunk in
	// (e.g. "rice", "menu_policy"). "always" is included on every turn.
	Tags []string `json:"tags"`
}

// botDefaultKnowledge seeds a tenant with no custom knowledge. It holds the
// natural-language rules that used to be pasted in full into every prompt.
func botDefaultKnowledge() []botKnowledgeChunk {
	return botMergeKnowledgeV2(botDefaultKnowledgeV1())
}

func botDefaultKnowledgeV1() []botKnowledgeChunk {
	return []botKnowledgeChunk{
		{ID: "menu_cerrado", Title: "Menú cerrado obligatorio", Tags: []string{"menu_policy", "create_booking", "rice"},
			Body: "El restaurante SOLO funciona con menú cerrado: menú del día (lunes a viernes) o menú de fin de semana (sábado y domingo). No existe carta libre ni platos sueltos. Cada comensal elige 1 entrante y 1 principal; el principal puede ser un plato principal o una ración de arroz. Si el cliente no quiere menú, explícalo con amabilidad y ofrece las opciones del menú."},
		{ID: "ninos", Title: "Niños y menú infantil", Tags: []string{"menu_policy", "special_needs"},
			Body: "No hay menú infantil ni carta para niños: es un menú por comensal, también para los niños que ocupan plaza. Explícalo con tacto, sin repetirlo si ya se dijo, y ofrece consultar el menú de ese día con get_menu_details o get_rice_menu. Los bebés que no comen no cuentan como comensal; ofrece tronas o carritos si vienen bebés."},
		{ID: "arroces", Title: "Arroces: reglas estrictas", Tags: []string{"rice"},
			Body: "Antes de hablar de arroces llama a get_rice_menu con la fecha de la reserva y usa SOLO la lista exacta devuelta. Coincidencia parcial con varias opciones: pregunta cuál. Arroz fuera de la lista: di que no lo tenemos y ofrece alternativas de la lista; nunca lo inventes ni lo traduzcas. Mínimo 2 raciones, al menos 2 personas de la mesa con arroz, y solo UNA variedad por mesa en mesas de menos de 8 personas. Algunos arroces tienen suplemento por ración: indícalo tal y como lo devuelve la herramienta. 'No', 'sin arroz' o 'no gracias' significa SIN ARROZ."},
		{ID: "arroz_reserva", Title: "Encargar el arroz con la reserva", Tags: []string{"rice", "booking_status", "modify_booking"},
			Body: "El arroz se recomienda encargar con antelación: si el cliente tiene una reserva y pregunta si debe decir el arroz, recomienda dejarlo indicado ya, ofrece la lista de get_rice_menu para su fecha y, cuando elija, repite los datos y actualiza la reserva con modify_booking (rice_type y rice_servings) tras su confirmación. No digas que 'da igual' o que 'puede decidirlo ese día'."},
		{ID: "reservas_existentes", Title: "Reservas ya hechas", Tags: []string{"booking_status", "modify_booking", "cancel_booking", "greeting"},
			Body: "El historial incluye confirmaciones y recordatorios automáticos. Si el cliente menciona 'la reserva' o datos que coinciden, se refiere a SU reserva: no le ofrezcas crear otra. Llama a get_bookings; si no la devuelve pero la confirmación está en el historial, responde con esos datos. Antes de modificar o cancelar obtén SIEMPRE el booking_id real con get_bookings."},
		{ID: "confirmacion", Title: "Confirmación explícita", Tags: []string{"create_booking", "modify_booking", "cancel_booking"},
			Body: "Antes de crear, modificar o cancelar repite los datos al cliente y espera su confirmación explícita ('sí', 'confirmo', 'adelante'). Solo entonces llama a la herramienta con confirmed=true. Una orden directa y clara del cliente sobre una reserva ya identificada ('anulo la reserva del sábado') cuenta como petición: confirma los datos concretos en un solo mensaje."},
		{ID: "disponibilidad", Title: "Disponibilidad y fechas", Tags: []string{"availability", "create_booking", "modify_booking"},
			Body: "Usa get_day_schedule y check_availability_for_party antes de aceptar una fecha: nunca inventes disponibilidad, horarios ni precios. 'El día 3' es la próxima fecha futura con ese número; si día de semana y número no coinciden, confirma la fecha completa. Nunca cambies la fecha pedida por otra. Si no hay hueco, propone alternativas e invita a la reserva online."},
		{ID: "mismo_dia", Title: "Gestiones del mismo día", Tags: []string{"create_booking", "modify_booking", "cancel_booking"},
			Body: "No puedes crear, modificar ni cancelar reservas para hoy; el sistema lo bloquea y envía el aviso y la tarjeta de contacto automáticamente. No dupliques ese aviso."},
		{ID: "alergenos", Title: "Alérgenos e ingredientes", Tags: []string{"allergens", "special_needs"},
			Body: "No dispones de información de ingredientes, caldos, elaboración ni alérgenos salvo que una herramienta la devuelva literalmente. Nunca supongas ni recomiendes platos como aptos. Ofrece anotar la alergia o intolerancia en los comentarios de su reserva y facilita el contacto del restaurante."},
		{ID: "necesidades", Title: "Necesidades especiales", Tags: []string{"special_needs"},
			Body: "Si el cliente menciona bebés, tronas, carritos, movilidad reducida, silla de ruedas, embarazo, celebración o alergias: reconócelo expresamente, anótalo en la reserva (high_chairs, baby_strollers o commentary) tras su confirmación y, si pide algo que no puedes garantizar (acceso, mesa concreta, tarta), di que lo dejas anotado para el restaurante y ofrece el contacto."},
		{ID: "extras", Title: "Extras de la reserva", Tags: []string{"extras"},
			Body: "Puedes informar de los extras disponibles con list_booking_extras, pero no puedes añadir, quitar ni modificar extras: es una gestión del restaurante."},
		{ID: "persona", Title: "Hablar con una persona", Tags: []string{"human", "complaint"},
			Body: "Si el cliente pide hablar con una persona o está molesto, discúlpate con brevedad sin excusas, envía la tarjeta de contacto con send_contact (una sola vez por respuesta) explicando en el campo message por qué se la pasas. Nunca envíes la tarjeta sola."},
		{ID: "cierre", Title: "Agradecimientos y cierres", Tags: []string{"acknowledgement"},
			Body: "Si el cliente solo agradece o confirma ('vale', 'gracias', '👍'), responde con una frase corta y cálida sin abrir temas nuevos ni repetir datos ya dados. Si hay una pregunta pendiente tuya, recuérdala en una línea."},
		{ID: "personal", Title: "Mensajes del personal", Tags: []string{"always"},
			Body: "Los mensajes marcados '[Mensaje escrito por el personal del restaurante]' los escribió una persona: respétalos, no los contradigas ni repitas. Los textos entre corchetes '[Aviso automático enviado: ...]' son acciones ya realizadas: nunca los copies."},
	}
}

// botKnowledgeV2 are chunks introduced with wa_bot_dspy_pipeline_v2. They are
// appended to tenants whose saved knowledge predates them (by id), so an
// existing restaurant gets the new rules without losing its edits.
func botKnowledgeV2() []botKnowledgeChunk {
	return []botKnowledgeChunk{
		{ID: "fechas_especiales", Title: "Fechas especiales", Tags: []string{"special_date", "create_booking", "availability"},
			Body: "Antes de hablar de una fecha usa get_date_overview. Si es FECHA ESPECIAL (Navidad, Nochevieja...) no se reserva por WhatsApp aunque haya plazas: explica el título, los menús y condiciones (pre-reserva, adelanto) y da el enlace booking_url de la web. Si el cliente ya tiene reserva en una fecha especial, no la modifiques ni canceles: gestión del restaurante con la tarjeta de contacto."},
		{ID: "eventos", Title: "Eventos y reservas especiales", Tags: []string{"event", "group_booking", "friendly_negotiation"},
			Body: "Bodas, comuniones, bautizos, empresas o banquetes y las reservas marcadas como evento se acuerdan siempre con la gestión del restaurante. No negocies precios, menús ni condiciones: recomienda llamar o escribir al teléfono de contacto."},
		{ID: "negociacion_cercana", Title: "Clientes valorando un evento", Tags: []string{"friendly_negotiation"},
			Body: "Si los comentarios de su reserva indican que viene a informarse de un evento o a una prueba de menú, sé cercano y abierto ('sin problema existiría la posibilidad de añadir un menú infantil para vosotros'), pero aclara siempre que tiene que confirmarlo con la dirección del restaurante en el teléfono de abajo y que tú no lo puedes asegurar al 100%."},
		{ID: "notas_reserva", Title: "Anotar en la reserva", Tags: []string{"special_needs", "allergens", "modify_booking"},
			Body: "Para dejar anotada una alergia, celebración, bebé o movilidad en una reserva normal usa add_booking_note tras confirmarlo con el cliente. No borra los comentarios del personal."},
		{ID: "audios", Title: "Audios", Tags: []string{"always"},
			Body: "Los mensajes que empiezan por '🎤 (audio transcrito)' son notas de voz transcritas automáticamente: respóndelas con normalidad; si algo no se entiende, pide que lo confirme por escrito."},
	}
}

func botMergeKnowledgeV2(chunks []botKnowledgeChunk) []botKnowledgeChunk {
	have := map[string]bool{}
	for _, c := range chunks {
		have[c.ID] = true
	}
	for _, c := range botKnowledgeV2() {
		if !have[c.ID] {
			chunks = append(chunks, c)
		}
	}
	return chunks
}

func botCleanKnowledge(in []botKnowledgeChunk) []botKnowledgeChunk {
	out := make([]botKnowledgeChunk, 0, len(in))
	seen := map[string]bool{}
	for i, c := range in {
		c.Title = strings.TrimSpace(c.Title)
		c.Body = strings.TrimSpace(c.Body)
		if c.Body == "" {
			continue
		}
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" || seen[c.ID] {
			c.ID = fmt.Sprintf("chunk_%d", i+1)
		}
		seen[c.ID] = true
		tags := c.Tags[:0]
		for _, t := range c.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
		c.Tags = tags
		out = append(out, c)
	}
	return out
}

// botKnowledgeIndex keeps the FTS5 index in sync with MySQL per restaurant.
type botKnowledgeIndex struct {
	mu      sync.Mutex
	db      *sql.DB
	indexed map[int]time.Time
}

func newBotKnowledgeIndex(db *sql.DB) *botKnowledgeIndex {
	idx := &botKnowledgeIndex{db: db, indexed: map[int]time.Time{}}
	if db == nil {
		return idx
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS bot_knowledge_fts USING fts5(
		restaurant_id UNINDEXED, chunk_id UNINDEXED, tags, title, body,
		tokenize = 'unicode61 remove_diacritics 2')`); err != nil {
		log.Printf("[bot] checkpoint wa_bot_rag_fts_v1 init_error=%v", err)
		idx.db = nil
	}
	return idx
}

func (k *botKnowledgeIndex) invalidate(restaurantID int) {
	if k == nil {
		return
	}
	k.mu.Lock()
	delete(k.indexed, restaurantID)
	k.mu.Unlock()
}

// ensure (re)indexes a restaurant's chunks at most every 5 minutes.
func (k *botKnowledgeIndex) ensure(ctx context.Context, restaurantID int, chunks []botKnowledgeChunk) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if t, ok := k.indexed[restaurantID]; ok && time.Since(t) < 5*time.Minute {
		return nil
	}
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM bot_knowledge_fts WHERE restaurant_id = ?`, restaurantID); err != nil {
		return err
	}
	for _, c := range chunks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_knowledge_fts (restaurant_id, chunk_id, tags, title, body) VALUES (?, ?, ?, ?, ?)`,
			restaurantID, c.ID, strings.Join(c.Tags, " "), c.Title, c.Body); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	k.indexed[restaurantID] = time.Now()
	return nil
}

var botFTSWordRe = regexp.MustCompile(`[\p{L}\p{N}]{3,}`)

// botFTSQuery turns free text into an OR query of quoted terms (FTS5 syntax
// characters in customer text can never break the query).
func botFTSQuery(text string) string {
	words := botFTSWordRe.FindAllString(strings.ToLower(text), 24)
	if len(words) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		quoted = append(quoted, `"`+w+`"`)
	}
	return strings.Join(quoted, " OR ")
}

// retrieve returns the chunks tagged for the given routes plus the top BM25
// matches for the text, deduplicated and capped.
func (k *botKnowledgeIndex) retrieve(ctx context.Context, restaurantID int, routes []string, text string, limit int) ([]botKnowledgeChunk, error) {
	type row struct{ id, title, body string }
	seen := map[string]bool{}
	var out []botKnowledgeChunk
	add := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.title, &r.body); err != nil {
				return err
			}
			if !seen[r.id] && len(out) < limit {
				seen[r.id] = true
				out = append(out, botKnowledgeChunk{ID: r.id, Title: r.title, Body: r.body})
			}
		}
		return rows.Err()
	}
	tagTerms := []string{`"always"`}
	for _, r := range routes {
		if w := botFTSWordRe.FindString(r); w != "" {
			tagTerms = append(tagTerms, `"`+strings.ToLower(r)+`"`)
		}
	}
	rows, err := k.db.QueryContext(ctx, `SELECT chunk_id, title, body FROM bot_knowledge_fts WHERE restaurant_id = ? AND bot_knowledge_fts MATCH ?`,
		restaurantID, "tags : ("+strings.Join(tagTerms, " OR ")+")")
	if err != nil {
		return nil, err
	}
	if err := add(rows); err != nil {
		return nil, err
	}
	// Free-text BM25 only supplements the route-tagged rules (max 2 extra),
	// so noisy words never crowd out the rules of the chosen route.
	if q := botFTSQuery(text); q != "" && len(out) < limit {
		limit = min(limit, len(out)+2)
		rows, err := k.db.QueryContext(ctx, `SELECT chunk_id, title, body FROM bot_knowledge_fts WHERE restaurant_id = ? AND bot_knowledge_fts MATCH ? ORDER BY bm25(bot_knowledge_fts, 0, 0, 0.5, 2.0, 1.0) LIMIT ?`,
			restaurantID, "{title body} : ("+q+")", limit+8)
		if err != nil {
			return out, err
		}
		if err := add(rows); err != nil {
			return out, err
		}
	}
	return out, nil
}

// botRetrieveKnowledge returns the relevant knowledge for this turn. On any
// index failure it degrades to the route-tagged chunks from memory so the
// rules are never silently dropped.
func (s *Server) botRetrieveKnowledge(ctx context.Context, restaurantID int, routing botAIRouting, routes []string, text string) []botKnowledgeChunk {
	chunks := routing.Knowledge
	if len(chunks) == 0 {
		chunks = botDefaultKnowledge()
	} else {
		chunks = botMergeKnowledgeV2(chunks)
	}
	if s.botKnowledge != nil && s.botKnowledge.db != nil {
		if err := s.botKnowledge.ensure(ctx, restaurantID, chunks); err == nil {
			if got, err := s.botKnowledge.retrieve(ctx, restaurantID, routes, text, 6); err == nil {
				return got
			} else {
				log.Printf("[bot] checkpoint wa_bot_rag_fts_v1 restaurant_id=%d retrieve_error=%v", restaurantID, err)
			}
		} else {
			log.Printf("[bot] checkpoint wa_bot_rag_fts_v1 restaurant_id=%d index_error=%v", restaurantID, err)
		}
	}
	want := map[string]bool{"always": true}
	for _, r := range routes {
		want[r] = true
	}
	var out []botKnowledgeChunk
	for _, c := range chunks {
		for _, t := range c.Tags {
			if want[t] {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
