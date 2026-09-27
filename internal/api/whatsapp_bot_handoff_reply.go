package api

import (
	"context"
	"hash/fnv"
	"log"
	"strings"
	"time"
)

// Coordination id: wa_bot_varied_handoff_reply_v1
//
// The acknowledgement sent to the customer when a request is forwarded to the
// management group (and the "already forwarded" reply) used to be one fixed
// sentence. It is now written per request by the tenant's LLM (primary with
// fallback, no tools, short timeout): it names the concrete topic, keeps the
// customer's language, and always ends offering further help. If the LLM
// fails or returns something unusable, a pool of hand-written variants is
// used, picked deterministically per request so consecutive replies differ.

var botForwardedVariantsES = []string{
	"Perfecto, he trasladado tu solicitud al equipo de gestión del restaurante y se pondrán en contacto contigo lo antes posible 🙌. Mientras tanto, ¿hay algo más en lo que pueda ayudarte?",
	"¡Listo! Ya le he pasado tu petición al equipo del restaurante; te escribirán o llamarán en breve 😊. ¿Te puedo ayudar con algo más mientras tanto?",
	"Anotado ✅. El equipo de gestión ya tiene tu solicitud y se pondrá en contacto contigo muy pronto. ¿Necesitas cualquier otra cosa?",
	"Hecho: he enviado tu consulta a las personas del restaurante que pueden resolverla y te contactarán enseguida 🙏. ¿Hay algo más en lo que te pueda echar una mano?",
	"Ya está en manos del equipo del restaurante 👌; se pondrán en contacto contigo lo antes posible. Mientras tanto, ¿quieres que te ayude con algo más?",
}

var botForwardedVariantsEN = []string{
	"Done! I've passed your request on to the restaurant management team and they will get in touch with you as soon as possible 🙌. In the meantime, is there anything else I can help you with?",
	"All set ✅. The restaurant team now has your request and will contact you shortly. Is there anything else I can do for you?",
	"I've sent your message to the people at the restaurant who can sort this out, and they'll reach out to you very soon 😊. Anything else I can help with?",
}

var botAlreadyVariantsES = []string{
	"Tu solicitud ya está en manos del equipo de gestión del restaurante y se pondrán en contacto contigo muy pronto 😊. Sobre este tema yo ya no puedo hacer nada más, pero si necesitas otra cosa, aquí estoy.",
	"Te entiendo 🙏. El equipo del restaurante ya tiene tu petición y te contactará en breve; sobre esto yo no puedo hacer nada más. ¿Te ayudo con cualquier otra cosa?",
	"No te preocupes, tu consulta ya la tiene el equipo de gestión y se pondrán en contacto contigo lo antes posible. Por mi parte no puedo hacer más sobre este tema, pero si necesitas algo distinto, dímelo 😊.",
	"Ya he avisado al equipo del restaurante y te van a contactar muy pronto 👌. Sobre este asunto yo ya no puedo hacer nada más; para cualquier otra cosa, aquí sigo.",
}

var botAlreadyVariantsEN = []string{
	"Your request is already with the restaurant management team and they will contact you very soon 😊. There's nothing more I can do about this topic myself, but if you need anything else, I'm here.",
	"I understand 🙏. The restaurant team already has your request and will get back to you shortly; I can't do anything more on this myself. Can I help with anything else?",
	"No worries, the management team already has your message and will reach out as soon as possible. There's nothing more I can do on this topic, but I'm happy to help with anything different 😊.",
}

// Same-day variants: the restaurant card is sent right after (wa_bot_same_day_card_v1).
var botSameDayVariantsES = []string{
	"Soy un asistente de reservas con IA y las gestiones de reservas para hoy no las puedo hacer por aquí. Ya he avisado al equipo del restaurante; para resolverlo hoy mismo, llámales con la tarjeta de contacto que te dejo justo debajo 👇. ¿Te ayudo con algo más?",
	"Las reservas del mismo día se gestionan por teléfono. He trasladado tu solicitud al equipo y te dejo aquí abajo la tarjeta del restaurante para que llames hoy 📞. ¿Necesitas algo más?",
	"Para cualquier cambio en una reserva de hoy lo más rápido es llamar al restaurante. Ya les he avisado y te paso su tarjeta de contacto a continuación 👇. ¿Puedo ayudarte con otra cosa?",
}

var botSameDayVariantsEN = []string{
	"I'm an AI booking assistant and I can't handle today's bookings here. I've let the restaurant team know; to sort it out today, please call them using the contact card below 👇. Anything else I can help with?",
	"Same-day bookings are handled by phone. I've passed your request to the team and here is the restaurant's contact card so you can call today 📞. Anything else?",
}

// botPickVariant chooses a variant from the pool, deterministic per request
// text + minute, so retries are stable but consecutive requests differ.
func botPickVariant(pool []string, seed string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed + time.Now().Format("200601021504")))
	return pool[int(h.Sum32())%len(pool)]
}

// botHandoffReplyText writes the customer acknowledgement for this request.
func (s *Server) botHandoffReplyText(ctx context.Context, restaurantID int, msg botWebhookMessage, reason, detail, prefix, lang string, duplicate bool) string {
	en := lang == "en" || lang == "other"
	pool := botForwardedVariantsES
	if duplicate {
		pool = botAlreadyVariantsES
	}
	if en {
		pool = botForwardedVariantsEN
		if duplicate {
			pool = botAlreadyVariantsEN
		}
	}
	if reason == "same_day" {
		pool = botSameDayVariantsES
		if en {
			pool = botSameDayVariantsEN
		}
		prefix = ""
	}
	fallback := botPickVariant(pool, msg.Sender+msg.Text+reason)
	if !duplicate && strings.TrimSpace(prefix) != "" {
		fallback = strings.TrimSpace(prefix) + "\n\n" + fallback
	}

	var sys strings.Builder
	sys.WriteString("Eres el asistente de reservas por WhatsApp de un restaurante. Escribe UN solo mensaje breve (2-3 frases, máximo 60 palabras) para el cliente.\n")
	if reason == "same_day" {
		// wa_bot_same_day_card_v1: same-day operations are solved by phone.
		if duplicate {
			sys.WriteString("Situación: el cliente insiste en crear, modificar o cancelar una reserva para HOY. El equipo del restaurante ya tiene su solicitud y ahora le vuelves a enviar la tarjeta de contacto del restaurante. Pídele con amabilidad que llame hoy mismo al restaurante usando la tarjeta que le dejas debajo, porque tú no puedes gestionar reservas del mismo día.\n")
		} else {
			sys.WriteString("Situación: el cliente quiere crear, modificar o cancelar una reserva para HOY, y eso no se puede hacer por WhatsApp. Has avisado al equipo del restaurante y le envías justo debajo la tarjeta de contacto del restaurante. Explícale brevemente que para gestiones del mismo día tiene que llamar hoy al restaurante usando esa tarjeta, y termina ofreciendo ayuda con cualquier otra cosa.\n")
		}
	} else if duplicate {
		sys.WriteString("Situación: el cliente vuelve a preguntar o insiste sobre un asunto que YA se trasladó al equipo de gestión del restaurante. Dile con amabilidad y empatía que el equipo ya tiene su solicitud y le contactará pronto, que sobre ese tema tú ya no puedes hacer nada más, y ofrécele ayuda con cualquier otra cosa.\n")
	} else {
		sys.WriteString("Situación: acabas de trasladar su solicitud al equipo de gestión del restaurante, que se pondrá en contacto con él lo antes posible. Confírmaselo mencionando brevemente el tema concreto de su petición y termina preguntando si puedes ayudarle con algo más.\n")
		if p := strings.TrimSpace(prefix); p != "" {
			sys.WriteString("Empieza explicando esto con tus palabras: " + p + "\n")
		}
	}
	sys.WriteString("Describe lo que el cliente PIDE (\"tu petición de cambiar la reserva a 8 personas\"), nunca lo presentes como un dato ya confirmado de su reserva (no digas \"tu reserva para 8 personas\"). Trata al cliente de tú salvo que él use usted.\n")
	sys.WriteString("Reglas: responde en el MISMO idioma que el mensaje del cliente; tono cercano y natural, sin repetir fórmulas hechas; como mucho un emoji; NO des teléfonos, emails ni horarios; NO prometas nada concreto (plazos, precios, que se hará el cambio); no uses Markdown salvo *negrita*; no inventes datos.\n")
	if d := strings.TrimSpace(detail); d != "" {
		sys.WriteString("Contexto interno (no lo cites literal): " + d + "\n")
	}
	sys.WriteString("Devuelve SOLO el texto del mensaje.")

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	routing := s.loadBotAIRouting(reqCtx, restaurantID)
	user := []botMessage{botUserText("Mensaje del cliente: " + truncate(msg.Text, 600))}
	// Two attempts: reasoning models occasionally spend the whole token
	// budget thinking and return no text; the retry is the same request.
	for attempt := 1; attempt <= 2; attempt++ {
		resp, used, err := s.botLLMCallRouted(reqCtx, restaurantID, routing, botSessionID(restaurantID, msg.Sender), sys.String(), user, nil)
		if err != nil {
			log.Printf("[bot] checkpoint wa_bot_varied_handoff_reply_v1 restaurant_id=%d attempt=%d llm_error=%v", restaurantID, attempt, err)
			continue
		}
		text := ""
		for _, b := range resp.Content {
			if b.Type == "text" {
				text += b.Text
			}
		}
		text = botCleanHandoffReply(text)
		if botHandoffReplyValid(text) {
			log.Printf("[bot] checkpoint wa_bot_varied_handoff_reply_v1 restaurant_id=%d model=%s duplicate=%t attempt=%d chars=%d", restaurantID, used, duplicate, attempt, len(text))
			return text
		}
		log.Printf("[bot] checkpoint wa_bot_varied_handoff_reply_v1 restaurant_id=%d attempt=%d invalid_llm_reply", restaurantID, attempt)
	}
	log.Printf("[bot] checkpoint wa_bot_varied_handoff_reply_v1 restaurant_id=%d fallback_variant=true", restaurantID)
	return fallback
}

// botCleanHandoffReply strips reasoning tags, quotes and surrounding noise.
func botCleanHandoffReply(t string) string {
	if i := strings.LastIndex(t, "</think>"); i >= 0 {
		t = t[i+len("</think>"):]
	}
	t = strings.TrimSpace(t)
	t = strings.Trim(t, "\"“”'`")
	return strings.TrimSpace(botWhatsAppFormat(t))
}

// botHandoffReplyValid rejects empty, overly long or unsafe (phone/email/
// link) replies so the customer never gets a broken message.
func botHandoffReplyValid(t string) bool {
	if len(t) < 25 || len(t) > 600 {
		return false
	}
	low := strings.ToLower(t)
	if strings.Contains(low, "@") || strings.Contains(low, "http") || strings.Contains(low, "<think") {
		return false
	}
	digits := 0
	for _, r := range t {
		if r >= '0' && r <= '9' {
			digits++
			if digits >= 7 {
				return false // looks like a phone number
			}
		} else if r != ' ' && r != '-' && r != '.' {
			digits = 0
		}
	}
	return true
}
