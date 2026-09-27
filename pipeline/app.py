"""WhatsApp bot decision pipeline (DSPy + Jev). Coordination id: wa_bot_dspy_pipeline_v2

The Go backend posts one inbound turn: text, recent history, deterministic DB
facts (customer bookings with staff commentary / event / special-date flags,
upcoming special dates, same-day, extras and allergen regex) and the open
sticky-handoff topic, if any. This service:

  1. asks Jev (TypeSafe System One) ONE batched request with every typed
     question the tree needs: intent (31 classes), anger meter, can-handle
     meter, event / negotiation meter, special-date choice, same-topic meter
     vs. the open handoff, special-needs and wants-note. One ~300 ms call
     replaces several LLM hops, which keeps the tree fast;
  2. only when Jev is unsure (confidence < 0.55) runs a DSPy Predict step over
     the tenant's primary/fallback LM to disambiguate with the history;
  3. walks an explicit if/else tree and returns the terminal action, RAG
     routes, a route directive and the visited node ids (rendered in
     /app/config -> Pipeline IA).

It also exposes /transcribe (faster-whisper) for WhatsApp voice notes
(wa_bot_audio_transcription_v1). Stateless and localhost-only.
"""
from __future__ import annotations

import base64
import json
import logging
import os
import tempfile
import threading
import time
import urllib.request
from typing import Any

import dspy
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

log = logging.getLogger("wa_bot_pipeline")
logging.basicConfig(level=logging.INFO, format="%(asctime)s [pipeline] %(message)s")
logging.getLogger("LiteLLM").setLevel(logging.ERROR)

JEV_URL = "https://api.typesafe.ai/v1/systemone"
OPENCODE_GO_BASE = "https://opencode.ai/zen/go/v1"
MINIMAX_ANTHROPIC_BASE = "https://api.minimax.io/anthropic"

# Thresholds (one place, easy to tune from real traffic in the graph tab).
CONFIDENCE_MIN = 0.55
ANGER_HANDOFF = 1.6        # score 0..3 (content, annoyed, clearly unhappy, very angry)
CAN_HANDLE_MIN = 0.30      # below -> human before giving a wrong answer
SAME_TOPIC_MIN = 0.55      # sticky handoff continues while the topic is the same
EVENT_MIN = 0.75

INTENTS: dict[str, str] = {
    "greeting": "Saludo sin petición concreta",
    "acknowledgement": "Agradece, confirma o cierra ('vale', 'gracias', 'ok', 'perfecto', emoji) sin pedir nada nuevo",
    "farewell": "Se despide",
    "info_hours": "Pregunta horarios o qué días abre el restaurante",
    "info_location": "Pregunta dirección, cómo llegar, aparcamiento o accesibilidad del local",
    "info_contact": "Pide el teléfono, email o web del restaurante",
    "menu_policy": "Pregunta cómo funciona el menú, si hay carta, platos sueltos, menú infantil o qué deben pedir los niños",
    "menu_content": "Pregunta qué platos, entrantes, postres o bebidas tiene el menú de un día",
    "prices": "Pregunta precios del menú, suplementos o cuánto cuesta",
    "rice": "Pregunta o pide arroces o paellas, encargar el arroz, raciones o suplementos de arroz",
    "availability": "Pregunta si hay sitio o disponibilidad sin pedir todavía la reserva",
    "create_booking": "Quiere hacer una reserva nueva o da datos para reservar (día, hora, personas)",
    "modify_booking": "Quiere cambiar una reserva existente (hora, fecha, personas, arroz, tronas)",
    "cancel_booking": "Quiere anular o cancelar una reserva",
    "booking_status": "Pregunta por su reserva existente o si está confirmada",
    "arrival_notice": "Avisa de su hora de llegada, de que llega tarde o confirma que acudirá",
    "allergens": "Pregunta por alergias, intolerancias, ingredientes o elaboración de los platos",
    "special_needs_request": "Pide tronas, carrito, mesa accesible, silla de ruedas, sitio para mascota u otra necesidad especial",
    "extras": "Pide añadir, quitar o cambiar extras de la reserva (café incluido, bebida ilimitada, tarta)",
    "special_date": "Pregunta por una fecha especial o festiva (Navidad, Nochevieja, Reyes, San Valentín, Día de la Madre)",
    "event_inquiry": "Quiere organizar o negociar un evento o celebración (boda, comunión, bautizo, empresa, cumpleaños de grupo)",
    "group_booking": "Reserva o consulta para un grupo grande (más de 12 personas)",
    "gift_voucher": "Pregunta por tarjetas o vales regalo",
    "invoice_payment": "Pide factura, ticket, pagos, adelantos, Bizum o devoluciones",
    "lost_item": "Ha perdido u olvidado un objeto en el restaurante",
    "job_application": "Busca trabajo o envía su currículum",
    "supplier": "Es un proveedor, comercial o empresa que ofrece servicios",
    "human": "Pide hablar con una persona, que le llamen o que le atienda alguien del restaurante",
    "complaint": "Se queja o expresa enfado con el restaurante, el servicio o el asistente",
    "feedback": "Da una opinión o valoración de una visita (positiva o negativa)",
    "other": "Cualquier otra cosa",
}

# Core intents the assistant is built for: the can-handle meter never vetoes
# them (Jev under-scores short follow-ups like "El sábado día 3"); the server
# policies (same day, special date, event) still guard the risky cases.
CORE_INTENTS = {"greeting", "acknowledgement", "farewell", "feedback", "info_hours", "availability", "create_booking",
                "modify_booking", "cancel_booking", "booking_status", "arrival_notice", "rice", "menu_policy", "menu_content", "group_booking"}

# Intents the assistant can never resolve by itself: always a human.
HUMAN_ONLY = {"invoice_payment", "lost_item", "job_application", "supplier", "gift_voucher", "event_inquiry", "human", "complaint"}

ROUTE_TAGS: dict[str, list[str]] = {
    "info_hours": ["info", "availability"], "info_location": ["info"], "info_contact": ["info"],
    "menu_content": ["menu_policy"], "prices": ["menu_policy", "rice"], "arrival_notice": ["booking_status"],
    "special_needs_request": ["special_needs", "modify_booking"], "special_date": ["special_date"],
    "group_booking": ["create_booking", "event"], "farewell": ["acknowledgement"], "feedback": ["complaint"],
}

GRAPH: dict[str, Any] = {
    "nodes": [
        {"id": "inbound", "label": "Mensaje entrante (texto o audio transcrito)", "kind": "start"},
        {"id": "jev_classify", "label": "Jev (1 llamada): intención · enfado · ¿puede resolverlo? · evento · fecha especial · mismo tema", "kind": "classifier"},
        {"id": "confident", "label": "¿Confianza Jev ≥ 0.55?", "kind": "decision"},
        {"id": "dspy_disambiguate", "label": "DSPy Predict: desambiguar con historial", "kind": "classifier"},
        {"id": "sticky_handoff", "label": "¿Tema derivado a humano abierto y el cliente sigue en el mismo tema?", "kind": "decision"},
        {"id": "handoff_repeat", "label": "Repetir con amabilidad: llamar a gestión + tarjeta", "kind": "handoff"},
        {"id": "same_day", "label": "¿Operación sobre reserva de HOY?", "kind": "decision"},
        {"id": "handoff_same_day", "label": "Aviso mismo día + tarjeta", "kind": "handoff"},
        {"id": "event_booking", "label": "¿Reserva marcada como EVENTO o negociación de evento?", "kind": "decision"},
        {"id": "handoff_event", "label": "Reserva especial: acordar con gestión + tarjeta", "kind": "handoff"},
        {"id": "special_date_check", "label": "¿Habla de una FECHA ESPECIAL?", "kind": "decision"},
        {"id": "special_date_booking", "label": "¿Ya tiene reserva esa fecha y quiere cambiarla/cancelarla?", "kind": "decision"},
        {"id": "handoff_special_booking", "label": "Fecha especial: no se modifica por WhatsApp + tarjeta gestión", "kind": "handoff"},
        {"id": "agent_special_date", "label": "Agente: info fecha especial + enlace pre-reserva web", "kind": "agent"},
        {"id": "extras_change", "label": "¿Pide cambiar extras?", "kind": "decision"},
        {"id": "handoff_extras", "label": "Aviso extras + tarjeta gestión", "kind": "handoff"},
        {"id": "allergen_question", "label": "¿Pregunta alérgenos/ingredientes (no pide anotarlo)?", "kind": "decision"},
        {"id": "handoff_allergens", "label": "Aviso seguridad alimentaria + tarjeta", "kind": "handoff"},
        {"id": "anger_meter", "label": "¿Enfado / insatisfacción alta?", "kind": "decision"},
        {"id": "can_handle", "label": "¿El asistente puede resolverlo con seguridad?", "kind": "decision"},
        {"id": "handoff_human", "label": "Disculpa breve + tarjeta de contacto (tema queda abierto)", "kind": "handoff"},
        {"id": "commentary_check", "label": "¿Comentarios de la reserva con evento/prueba de menú?", "kind": "decision"},
        {"id": "tag_friendly", "label": "Tono cercano y abierto, sin prometer (confirmar con gestión)", "kind": "enrich"},
        {"id": "special_needs", "label": "¿Necesidad especial (niños, movilidad, alergia…)?", "kind": "decision"},
        {"id": "tag_special_needs", "label": "Añadir reglas de necesidades especiales", "kind": "enrich"},
        {"id": "route_intent", "label": "Enrutar por intención", "kind": "decision"},
        {"id": "agent_booking", "label": "Agente: reserva (crear/modificar/cancelar)", "kind": "agent"},
        {"id": "agent_rice", "label": "Agente: arroces", "kind": "agent"},
        {"id": "agent_menu", "label": "Agente: menú, platos y precios", "kind": "agent"},
        {"id": "agent_availability", "label": "Agente: disponibilidad", "kind": "agent"},
        {"id": "agent_status", "label": "Agente: reserva existente / llegada", "kind": "agent"},
        {"id": "agent_short_reply", "label": "Agente: respuesta corta de cierre", "kind": "agent"},
        {"id": "agent_general", "label": "Agente: información general", "kind": "agent"},
    ],
    "edges": [
        {"from": "inbound", "to": "jev_classify"},
        {"from": "jev_classify", "to": "confident"},
        {"from": "confident", "to": "sticky_handoff", "label": "sí"},
        {"from": "confident", "to": "dspy_disambiguate", "label": "no"},
        {"from": "dspy_disambiguate", "to": "sticky_handoff"},
        {"from": "sticky_handoff", "to": "handoff_repeat", "label": "sí"},
        {"from": "sticky_handoff", "to": "same_day", "label": "no / tema nuevo"},
        {"from": "same_day", "to": "handoff_same_day", "label": "sí"},
        {"from": "same_day", "to": "event_booking", "label": "no"},
        {"from": "event_booking", "to": "handoff_event", "label": "sí"},
        {"from": "event_booking", "to": "special_date_check", "label": "no"},
        {"from": "special_date_check", "to": "special_date_booking", "label": "sí"},
        {"from": "special_date_check", "to": "extras_change", "label": "no"},
        {"from": "special_date_booking", "to": "handoff_special_booking", "label": "sí"},
        {"from": "special_date_booking", "to": "agent_special_date", "label": "no"},
        {"from": "extras_change", "to": "handoff_extras", "label": "sí"},
        {"from": "extras_change", "to": "allergen_question", "label": "no"},
        {"from": "allergen_question", "to": "handoff_allergens", "label": "sí"},
        {"from": "allergen_question", "to": "anger_meter", "label": "no"},
        {"from": "anger_meter", "to": "handoff_human", "label": "sí"},
        {"from": "anger_meter", "to": "can_handle", "label": "no"},
        {"from": "can_handle", "to": "handoff_human", "label": "no"},
        {"from": "can_handle", "to": "commentary_check", "label": "sí"},
        {"from": "commentary_check", "to": "tag_friendly", "label": "sí"},
        {"from": "commentary_check", "to": "special_needs", "label": "no"},
        {"from": "tag_friendly", "to": "special_needs"},
        {"from": "special_needs", "to": "tag_special_needs", "label": "sí"},
        {"from": "special_needs", "to": "route_intent", "label": "no"},
        {"from": "tag_special_needs", "to": "route_intent"},
        {"from": "route_intent", "to": "agent_booking", "label": "crear/modificar/cancelar"},
        {"from": "route_intent", "to": "agent_rice", "label": "arroz"},
        {"from": "route_intent", "to": "agent_menu", "label": "menú/precios"},
        {"from": "route_intent", "to": "agent_availability", "label": "disponibilidad"},
        {"from": "route_intent", "to": "agent_status", "label": "mi reserva/llegada"},
        {"from": "route_intent", "to": "agent_short_reply", "label": "gracias/adiós"},
        {"from": "route_intent", "to": "agent_general", "label": "saludo/info/otro"},
    ],
}

DIRECTIVES: dict[str, str] = {
    "agent_booking": "Gestión de reserva: usa get_date_overview para la fecha (si es fecha especial NO se reserva por WhatsApp), identifica la reserva con get_bookings si ya existe, repite los datos y pide confirmación explícita antes de ejecutar.",
    "agent_rice": "Consulta sobre arroces: llama a get_rice_menu con la fecha de la reserva (get_bookings si tiene una) y responde SOLO con la lista exacta; recomienda dejarlo encargado ya en la reserva.",
    "agent_menu": "Consulta sobre el menú: explica el menú cerrado por comensal y usa list_menus / get_menu_details / get_booking_menu para platos y precios; no inventes nada.",
    "agent_availability": "Consulta de disponibilidad: usa get_date_overview con la fecha y el número de personas antes de responder y ofrece reservar (si es fecha especial, da el enlace de la web).",
    "agent_status": "Pregunta sobre su reserva existente: consulta get_bookings/get_booking_details y responde con los datos reales. Si solo avisa de su llegada, agradéceselo en una frase.",
    "agent_short_reply": "El cliente solo agradece, confirma o se despide: responde en UNA frase corta y cálida, sin abrir temas nuevos.",
    "agent_general": "Responde con precisión usando las herramientas; no inventes datos.",
    "agent_special_date": "El cliente pregunta por una FECHA ESPECIAL: usa get_date_overview con esa fecha, explica título, menús y condiciones (pre-reserva, adelanto) y da SIEMPRE el enlace booking_url de la web para reservar. Nunca crees la reserva por WhatsApp.",
}
FRIENDLY_DIRECTIVE = ("Los comentarios del personal indican que este cliente está valorando un evento o una prueba de menú: sé especialmente cercano y abierto "
                      "(p. ej. 'sin problema existiría la posibilidad de…'), pero NUNCA lo asegures: indica siempre que debe confirmarlo con la dirección del "
                      "restaurante en el teléfono de contacto y que tú no lo puedes garantizar al 100%.")
SPECIAL_NEEDS_DIRECTIVE = "El cliente ha mencionado una necesidad especial: reconócela expresamente y ofrece anotarla en la reserva con add_booking_note."

HANDOFF_TEXTS = {
    "anger": "Siento mucho que no te haya podido ayudar como esperabas 🙏. Soy un asistente de reservas con Inteligencia Artificial y lo mejor es que te atienda directamente una persona de la gestión del restaurante. Te dejo su contacto 👇",
    "cannot": "Soy un asistente de reservas con Inteligencia Artificial y esta consulta no la puedo resolver con seguridad; prefiero no darte una respuesta equivocada. Para esto, contacta directamente con la gestión del restaurante, llamando o escribiendo al teléfono que te dejo a continuación 👇",
    "repeat": "Entiendo tu insistencia y lo siento de verdad, pero este tema no lo puedo resolver yo por aquí. Te recomiendo llamar o escribir a la gestión del restaurante, que podrá ayudarte personalmente en el teléfono que te dejo de nuevo 👇. Si necesitas cualquier otra cosa distinta, aquí estoy.",
}

# ----------------------------------------------------------------- Jev -----


def jev_classify(api_key: str, state: str, handoff_topic: str, special_dates: dict[str, str]) -> dict[str, Any]:
    questions: dict[str, Any] = {
        "intent": {"type": "choice", "instructions": "Intención principal del ÚLTIMO mensaje del cliente a un restaurante por WhatsApp (el historial es solo contexto)", "criteria": INTENTS},
        "anger": {"type": "score", "instructions": "Enfado o insatisfacción del cliente con el restaurante o con las respuestas del asistente en el último mensaje (teniendo en cuenta si insiste o repite)",
                  "criteria": ["Contento o neutro", "Algo molesto o insistente", "Claramente molesto o insatisfecho", "Muy enfadado"]},
        "can_handle": {"type": "noul", "instructions": "Un asistente de reservas de restaurante que SOLO puede consultar horarios, disponibilidad, fechas especiales, menús y arroces publicados, crear/modificar/cancelar reservas normales y anotar comentarios en ellas puede resolver por completo, con datos reales y sin inventar, lo que pide el último mensaje"},
        "event": {"type": "noul", "instructions": "El cliente habla de organizar o negociar un evento o celebración (boda, comunión, bautizo, empresa, banquete, cumpleaños de grupo) o pide condiciones especiales de precio o menú"},
        "special_needs": {"type": "noul", "instructions": "El último mensaje menciona una necesidad especial: niños o bebés, tronas o carritos, movilidad reducida o silla de ruedas, alergia o intolerancia, embarazo, mascota o celebración"},
        "wants_note": {"type": "noul", "instructions": "El cliente pide anotar o apuntar algo (por ejemplo una alergia) en los comentarios de su reserva"},
    }
    if handoff_topic:
        questions["same_topic"] = {"type": "noul", "instructions": "El último mensaje del cliente sigue tratando, insistiendo o preguntando sobre este mismo asunto: " + handoff_topic}
    if special_dates:
        crit = dict(special_dates)
        crit["none"] = "No se refiere a ninguna de estas fechas especiales"
        questions["special_date"] = {"type": "choice", "instructions": "Fecha especial a la que se refiere el último mensaje del cliente", "criteria": crit}
    body = {"state": state, "model": "jev-latest", "questions": questions}
    req = urllib.request.Request(JEV_URL, json.dumps(body).encode(), {"Authorization": "Bearer " + api_key, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=6) as resp:
        a = json.load(resp)["answers"]
    out = {
        "intent": a["intent"]["choice"], "confidence": float(a["intent"].get("confidence", 0)),
        "anger": float(a["anger"]["score"]), "can_handle": float(a["can_handle"]["noul"]), "event": float(a["event"]["noul"]),
        "special_needs": float(a["special_needs"]["noul"]), "wants_note": float(a["wants_note"]["noul"]),
    }
    if "same_topic" in a:
        out["same_topic"] = float(a["same_topic"]["noul"])
    if "special_date" in a:
        out["special_date"] = a["special_date"]["choice"]
        out["special_date_confidence"] = float(a["special_date"].get("confidence", 0))
    return out


class JevCategorizer(dspy.Module):
    """DSPy module wrapping one batched Jev System One call."""

    def forward(self, api_key: str, state: str, handoff_topic: str, special_dates: dict[str, str]) -> dspy.Prediction:
        return dspy.Prediction(**jev_classify(api_key, state, handoff_topic, special_dates))


class Disambiguate(dspy.Signature):
    """Clasifica la intención del ÚLTIMO mensaje de un cliente de restaurante por WhatsApp usando el historial."""

    history: str = dspy.InputField(desc="Últimos mensajes de la conversación")
    message: str = dspy.InputField(desc="Último mensaje del cliente")
    candidates: str = dspy.InputField(desc="Intenciones posibles con su descripción")
    intent: str = dspy.OutputField(desc="Exactamente una clave de las intenciones posibles")


# ------------------------------------------------------------ pipeline -----


class LMSpec(BaseModel):
    provider: str
    model: str
    api_key: str = ""


class TurnRequest(BaseModel):
    restaurant_id: int
    session_id: str
    text: str
    history: list[str] = Field(default_factory=list)
    facts: dict[str, Any] = Field(default_factory=dict)
    jev_api_key: str = ""
    lms: list[LMSpec] = Field(default_factory=list)


def build_lm(spec: LMSpec, session_id: str) -> dspy.LM | None:
    if not spec.api_key:
        return None
    if spec.provider == "opencode-go":
        return dspy.LM("openai/" + spec.model, api_base=OPENCODE_GO_BASE, api_key=spec.api_key, max_tokens=600,
                       extra_headers={"x-opencode-session": session_id}, cache=False, num_retries=0, timeout=8)
    if spec.provider == "minimax":
        return dspy.LM("anthropic/" + spec.model, api_base=MINIMAX_ANTHROPIC_BASE, api_key=spec.api_key, max_tokens=600,
                       cache=False, num_retries=0, timeout=8)
    return None


class BotPipeline(dspy.Module):
    def __init__(self) -> None:
        super().__init__()
        self.jev = JevCategorizer()
        self.disambiguate = dspy.Predict(Disambiguate)

    def forward(self, req: TurnRequest) -> dict[str, Any]:
        path: list[str] = ["inbound"]
        facts = req.facts
        recent = req.history[-8:]
        state = ("Historial:\n" + "\n".join(recent) + "\n\n" if recent else "") + "Último mensaje del cliente: " + req.text
        handoff = facts.get("handoff") or {}
        handoff_topic = str(handoff.get("topic") or "")
        special_dates = {d["key"]: d["label"] for d in facts.get("special_dates") or [] if d.get("key")}
        bookings = facts.get("bookings") or []

        path.append("jev_classify")
        jev: dict[str, Any] = {}
        try:
            if req.jev_api_key:
                jev = self.jev(api_key=req.jev_api_key, state=state, handoff_topic=handoff_topic, special_dates=special_dates).toDict()
        except Exception as exc:  # noqa: BLE001 - never block the turn on Jev
            log.warning("jev_failed restaurant_id=%s err=%s", req.restaurant_id, exc)
        intent = jev.get("intent") or {"create_booking": "create_booking", "modify_booking": "modify_booking", "cancel_booking": "cancel_booking"}.get(facts.get("regex_intent") or "", "other")
        confidence = float(jev.get("confidence", 0))
        classifier = "jev" if jev else "regex"

        path.append("confident")
        if confidence < CONFIDENCE_MIN:
            path.append("dspy_disambiguate")
            for spec in req.lms:
                lm = build_lm(spec, req.session_id)
                if lm is None:
                    continue
                try:
                    with dspy.context(lm=lm, adapter=dspy.JSONAdapter()):
                        pred = self.disambiguate(history="\n".join(recent) or "(sin historial)", message=req.text,
                                                 candidates="\n".join(f"{k}: {v}" for k, v in INTENTS.items()))
                    guess = str(pred.intent).strip().strip("`'\" ").lower()
                    if guess in INTENTS:
                        intent, classifier = guess, "dspy:" + spec.provider
                        break
                except Exception as exc:  # noqa: BLE001
                    log.warning("dspy_disambiguate_failed provider=%s err=%s", spec.provider, exc)

        anger = float(jev.get("anger", 0))
        can_handle = float(jev.get("can_handle", 1))
        result: dict[str, Any] = {"intent": intent, "confidence": confidence, "classifier": classifier, "jev": jev,
                                  "anger": anger, "can_handle": can_handle}

        def done(action: str, node: str, routes: list[str], directive: str = "", **extra: Any) -> dict[str, Any]:
            path.append(node)
            result.update(action=action, node=node, routes=routes, directive=directive, path=path, **extra)
            return result

        # sticky_handoff: the topic already belongs to a human.
        path.append("sticky_handoff")
        if handoff_topic:
            same = float(jev.get("same_topic", 1.0 if not jev else 0.0))
            closing = intent in ("acknowledgement", "farewell", "greeting") and same < 0.8
            if same >= SAME_TOPIC_MIN and not closing:
                return done("handoff_human", "handoff_repeat", [], handoff_reason="repeat", handoff_text=HANDOFF_TEXTS["repeat"], handoff_topic=handoff_topic)
            result["handoff_cleared"] = True

        path.append("same_day")
        if facts.get("same_day_intent"):
            return done("handoff_same_day", "handoff_same_day", [])

        # event_booking: staff flag (is_event) or an event negotiation.
        path.append("event_booking")
        event_booking = next((b for b in bookings if b.get("is_event")), None)
        mentions_booking = intent in ("modify_booking", "cancel_booking", "booking_status", "menu_policy", "menu_content", "prices", "rice",
                                      "special_needs_request", "extras", "group_booking", "event_inquiry", "allergens")
        if event_booking and mentions_booking and (len(bookings) == 1 or float(jev.get("event", 0)) >= 0.5 or intent in ("modify_booking", "cancel_booking")):
            return done("handoff_event", "handoff_event", [], handoff_topic="Detalles de la reserva de evento del " + str(event_booking.get("date")))
        # Staff commentary says the customer is evaluating an event / menu
        # tasting (but the booking is not flagged as event): answer warmly and
        # openly instead of a hard handoff; only an explicit event inquiry
        # still goes to management.
        negotiating = any("event_negotiation" in (b.get("commentary_signals") or []) for b in bookings)
        if intent == "event_inquiry" or (not negotiating and float(jev.get("event", 0)) >= EVENT_MIN and intent in ("create_booking", "group_booking", "prices", "menu_policy")):
            return done("handoff_event", "handoff_event", [], handoff_topic="Organización o negociación de un evento")

        # special_date_check
        path.append("special_date_check")
        sd_key = str(jev.get("special_date") or "none")
        special = next((d for d in facts.get("special_dates") or [] if d.get("key") == sd_key), None)
        if special is None and intent == "special_date" and len(facts.get("special_dates") or []) == 1:
            special = facts["special_dates"][0]
        if special is not None:
            path.append("special_date_booking")
            has_booking = any(b.get("date") == special.get("date") for b in bookings)
            if has_booking and intent in ("modify_booking", "cancel_booking", "special_needs_request", "extras", "rice"):
                return done("handoff_special_booking", "handoff_special_booking", [], special_date=special.get("date"),
                            handoff_topic="Cambios en la reserva de la fecha especial " + str(special.get("label")))
            return done("agent", "agent_special_date", ["special_date", "create_booking"],
                        DIRECTIVES["agent_special_date"] + f" Fecha: {special.get('date')} ({special.get('label')}).", special_date=special.get("date"))

        path.append("extras_change")
        if facts.get("regex_extras_mutation") or (intent == "extras" and confidence >= 0.7 and facts.get("mentions_extras")):
            return done("handoff_extras", "handoff_extras", [])

        path.append("allergen_question")
        wants_note = float(jev.get("wants_note", 0)) >= 0.6 or bool(facts.get("regex_booking_note"))
        if not wants_note and (facts.get("regex_allergen") or (intent == "allergens" and confidence >= 0.6)):
            return done("handoff_allergens", "handoff_allergens", [], handoff_topic="Alérgenos o ingredientes de los platos")

        path.append("anger_meter")
        if anger >= ANGER_HANDOFF or intent in ("human", "complaint"):
            return done("handoff_human", "handoff_human", [], handoff_reason="anger", handoff_text=HANDOFF_TEXTS["anger"],
                        handoff_topic="Cliente insatisfecho o que pide una persona: " + req.text[:160])

        path.append("can_handle")
        soft_ok = negotiating and intent in ("menu_policy", "menu_content", "prices", "rice", "special_needs_request", "booking_status", "availability")
        if intent in HUMAN_ONLY or (jev and not soft_ok and can_handle < CAN_HANDLE_MIN and intent not in CORE_INTENTS):
            return done("handoff_human", "handoff_human", [], handoff_reason="cannot", handoff_text=HANDOFF_TEXTS["cannot"],
                        handoff_topic=INTENTS.get(intent, intent) + ": " + req.text[:160])

        path.append("commentary_check")
        directive_extra = ""
        routes = list(ROUTE_TAGS.get(intent, [intent]))
        if negotiating:
            path.append("tag_friendly")
            directive_extra += " " + FRIENDLY_DIRECTIVE
            routes.append("friendly_negotiation")

        path.append("special_needs")
        if float(jev.get("special_needs", 0)) >= 0.6 or wants_note:
            path.append("tag_special_needs")
            routes.append("special_needs")
            directive_extra += " " + SPECIAL_NEEDS_DIRECTIVE

        path.append("route_intent")
        node = {
            "create_booking": "agent_booking", "modify_booking": "agent_booking", "cancel_booking": "agent_booking", "group_booking": "agent_booking",
            "special_needs_request": "agent_booking", "rice": "agent_rice", "menu_policy": "agent_menu", "menu_content": "agent_menu", "prices": "agent_menu",
            "availability": "agent_availability", "info_hours": "agent_availability", "booking_status": "agent_status", "arrival_notice": "agent_status",
            "acknowledgement": "agent_short_reply", "farewell": "agent_short_reply",
        }.get(intent, "agent_general")
        return done("agent", node, routes, DIRECTIVES[node] + directive_extra)


PIPELINE = BotPipeline()
app = FastAPI(title="wa-bot-pipeline")


@app.get("/healthz")
def healthz() -> dict[str, Any]:
    return {"ok": True, "dspy": dspy.__version__, "whisper_model": WHISPER_MODEL_NAME}


@app.get("/graph")
def graph() -> dict[str, Any]:
    return GRAPH


@app.post("/decide")
def decide(req: TurnRequest) -> dict[str, Any]:
    started = time.time()
    out = PIPELINE(req)
    out["elapsed_ms"] = int((time.time() - started) * 1000)
    log.info("checkpoint wa_bot_dspy_pipeline_v2 restaurant_id=%s session=%s intent=%s conf=%.2f anger=%.2f can=%.2f node=%s ms=%s",
             req.restaurant_id, req.session_id, out["intent"], out["confidence"], out["anger"], out["can_handle"], out["node"], out["elapsed_ms"])
    return out


# --------------------------------------------------------- transcription ----
WHISPER_MODEL_NAME = os.environ.get("WHISPER_MODEL", "small")
_whisper = None
_whisper_lock = threading.Lock()


def whisper_model():
    global _whisper
    with _whisper_lock:
        if _whisper is None:
            from faster_whisper import WhisperModel
            _whisper = WhisperModel(WHISPER_MODEL_NAME, device="cpu", compute_type="int8", cpu_threads=int(os.environ.get("WHISPER_THREADS", "4")))
        return _whisper


class TranscribeRequest(BaseModel):
    audio_b64: str
    restaurant_id: int = 0


@app.post("/transcribe")
def transcribe(req: TranscribeRequest) -> dict[str, Any]:
    started = time.time()
    raw = req.audio_b64.split(",", 1)[-1]
    try:
        data = base64.b64decode(raw)
    except Exception as exc:  # noqa: BLE001
        raise HTTPException(400, "invalid base64") from exc
    if not data or len(data) > 8 << 20:
        raise HTTPException(400, "empty or too large")
    with tempfile.NamedTemporaryFile(suffix=".ogg") as f:
        f.write(data)
        f.flush()
        segments, info = whisper_model().transcribe(f.name, beam_size=1, vad_filter=True, initial_prompt="Conversación con un restaurante: reserva, arroz, menú, personas, hora.")
        text = " ".join(s.text.strip() for s in segments).strip()
    ms = int((time.time() - started) * 1000)
    log.info("checkpoint wa_bot_audio_transcription_v1 restaurant_id=%s lang=%s chars=%s ms=%s", req.restaurant_id, info.language, len(text), ms)
    return {"text": text, "language": info.language, "duration_s": round(info.duration, 1), "elapsed_ms": ms}


@app.on_event("startup")
def _warm() -> None:
    threading.Thread(target=whisper_model, daemon=True).start()
