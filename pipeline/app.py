"""WhatsApp bot decision pipeline (DSPy). Coordination id: wa_bot_dspy_pipeline_v1

The Go backend posts one inbound turn (text, short history, deterministic DB
facts, tenant keys/models). This service classifies it with Jev (TypeSafe
System One: Choice/Noul/Score), resolves ambiguous cases with a DSPy
ChainOfThought, and walks an explicit if/else decision tree. It returns the
terminal action, the knowledge routes for the RAG and the path of node ids
taken, which the backoffice renders as a graph (/app/config -> Pipeline).

Stateless and localhost-only: keys arrive per request and are never stored.
"""
from __future__ import annotations

import json
import logging
import time
import urllib.request
from typing import Any

import dspy
from fastapi import FastAPI
from pydantic import BaseModel, Field

log = logging.getLogger("wa_bot_pipeline")
logging.basicConfig(level=logging.INFO, format="%(asctime)s [pipeline] %(message)s")
logging.getLogger("LiteLLM").setLevel(logging.ERROR)

JEV_URL = "https://api.typesafe.ai/v1/systemone"
OPENCODE_GO_BASE = "https://opencode.ai/zen/go/v1"
MINIMAX_ANTHROPIC_BASE = "https://api.minimax.io/anthropic"

INTENTS: dict[str, str] = {
    "greeting": "Saludo sin petición concreta",
    "acknowledgement": "Agradece, confirma o cierra ('vale', 'gracias', 'ok', 'perfecto', emoji) sin pedir nada nuevo",
    "info": "Pregunta horarios, ubicación, aparcamiento, contacto, precios o información general del restaurante",
    "menu_policy": "Pregunta cómo funciona el menú, la carta, platos sueltos, menú infantil o qué deben pedir los niños",
    "rice": "Pregunta o pide arroces o paellas, encargar el arroz, raciones o suplementos de arroz",
    "availability": "Pregunta si hay sitio o disponibilidad sin pedir todavía la reserva",
    "create_booking": "Quiere hacer una reserva nueva o da datos para reservar (día, hora, personas)",
    "modify_booking": "Quiere cambiar una reserva existente (hora, fecha, personas, arroz, tronas)",
    "cancel_booking": "Quiere anular o cancelar una reserva",
    "booking_status": "Pregunta por su reserva existente, confirma asistencia o avisa de su hora de llegada",
    "allergens": "Pregunta por alergias, intolerancias, ingredientes o elaboración de los platos",
    "extras": "Pide añadir, quitar o cambiar extras de la reserva (café incluido, bebida ilimitada, tarta)",
    "human": "Pide hablar con una persona, que le llamen o que le atienda alguien del restaurante",
    "complaint": "Se queja o expresa enfado con el restaurante o el servicio",
    "other": "Cualquier otra cosa",
}

# Terminal routes -> knowledge tags for the RAG (wa_bot_rag_fts_v1).
ROUTE_TAGS: dict[str, list[str]] = {i: [i] for i in INTENTS}
ROUTE_TAGS["other"] = []

# ---------------------------------------------------------------- graph ----
# Declarative tree rendered by the backoffice. Every id here is emitted in
# `path` when the node is visited, so the UI can highlight real traffic.
GRAPH: dict[str, Any] = {
    "nodes": [
        {"id": "inbound", "label": "Mensaje entrante", "kind": "start"},
        {"id": "jev_classify", "label": "Jev: intención + necesidades + frustración", "kind": "classifier"},
        {"id": "confident", "label": "¿Confianza Jev ≥ 0.55?", "kind": "decision"},
        {"id": "dspy_disambiguate", "label": "DSPy Predict: desambiguar con historial", "kind": "classifier"},
        {"id": "same_day", "label": "¿Operación sobre reserva de HOY?", "kind": "decision"},
        {"id": "handoff_same_day", "label": "Aviso mismo día + tarjeta de contacto", "kind": "handoff"},
        {"id": "extras_change", "label": "¿Pide cambiar extras?", "kind": "decision"},
        {"id": "handoff_extras", "label": "Aviso extras + tarjeta de gestión", "kind": "handoff"},
        {"id": "allergen_question", "label": "¿Pregunta alérgenos/ingredientes (no pide anotarlo)?", "kind": "decision"},
        {"id": "handoff_allergens", "label": "Aviso seguridad alimentaria + tarjeta", "kind": "handoff"},
        {"id": "escalate", "label": "¿Pide persona o frustración alta?", "kind": "decision"},
        {"id": "agent_human", "label": "Agente: disculpa breve + send_contact", "kind": "agent"},
        {"id": "special_needs", "label": "¿Necesidad especial (niños, movilidad, alergia…)?", "kind": "decision"},
        {"id": "tag_special_needs", "label": "Añadir reglas de necesidades especiales", "kind": "enrich"},
        {"id": "route_intent", "label": "Enrutar por intención", "kind": "decision"},
        {"id": "agent_booking", "label": "Agente: reserva (crear/modificar/cancelar)", "kind": "agent"},
        {"id": "agent_rice", "label": "Agente: arroces (get_rice_menu)", "kind": "agent"},
        {"id": "agent_menu_policy", "label": "Agente: política de menú", "kind": "agent"},
        {"id": "agent_availability", "label": "Agente: disponibilidad", "kind": "agent"},
        {"id": "agent_status", "label": "Agente: reserva existente", "kind": "agent"},
        {"id": "agent_short_reply", "label": "Agente: respuesta corta de cierre", "kind": "agent"},
        {"id": "agent_general", "label": "Agente: información general", "kind": "agent"},
    ],
    "edges": [
        {"from": "inbound", "to": "jev_classify"},
        {"from": "jev_classify", "to": "confident"},
        {"from": "confident", "to": "same_day", "label": "sí"},
        {"from": "confident", "to": "dspy_disambiguate", "label": "no"},
        {"from": "dspy_disambiguate", "to": "same_day"},
        {"from": "same_day", "to": "handoff_same_day", "label": "sí"},
        {"from": "same_day", "to": "extras_change", "label": "no"},
        {"from": "extras_change", "to": "handoff_extras", "label": "sí"},
        {"from": "extras_change", "to": "allergen_question", "label": "no"},
        {"from": "allergen_question", "to": "handoff_allergens", "label": "sí"},
        {"from": "allergen_question", "to": "escalate", "label": "no"},
        {"from": "escalate", "to": "agent_human", "label": "sí"},
        {"from": "escalate", "to": "special_needs", "label": "no"},
        {"from": "special_needs", "to": "tag_special_needs", "label": "sí"},
        {"from": "special_needs", "to": "route_intent", "label": "no"},
        {"from": "tag_special_needs", "to": "route_intent"},
        {"from": "route_intent", "to": "agent_booking", "label": "crear/modificar/cancelar"},
        {"from": "route_intent", "to": "agent_rice", "label": "arroz"},
        {"from": "route_intent", "to": "agent_menu_policy", "label": "menú"},
        {"from": "route_intent", "to": "agent_availability", "label": "disponibilidad"},
        {"from": "route_intent", "to": "agent_status", "label": "mi reserva"},
        {"from": "route_intent", "to": "agent_short_reply", "label": "gracias/vale"},
        {"from": "route_intent", "to": "agent_general", "label": "saludo/info/otro"},
    ],
}

# Per-route instruction injected into the agent turn (short, specific).
DIRECTIVES: dict[str, str] = {
    "agent_human": "El cliente quiere una persona o está molesto: discúlpate en una frase, sin excusas, y llama a send_contact UNA vez explicando por qué le pasas el contacto.",
    "agent_booking": "Gestión de reserva: identifica la reserva con get_bookings si ya existe, comprueba disponibilidad con las herramientas, repite los datos y pide confirmación explícita antes de ejecutar.",
    "agent_rice": "Consulta sobre arroces: llama a get_rice_menu con la fecha de la reserva (usa get_bookings si tiene una) y responde SOLO con la lista exacta; recomienda encargarlo ya en la reserva.",
    "agent_menu_policy": "Consulta sobre el funcionamiento del menú: explica el menú cerrado por comensal con claridad y empatía, sin repetir lo ya dicho en el historial.",
    "agent_availability": "Consulta de disponibilidad: comprueba con get_day_schedule y check_availability_for_party antes de responder y ofrece reservar.",
    "agent_status": "Pregunta sobre su reserva existente: consulta get_bookings y responde con los datos reales.",
    "agent_short_reply": "El cliente solo agradece o confirma: responde en UNA frase corta y cálida, sin abrir temas nuevos.",
    "agent_general": "Responde con precisión usando las herramientas; no inventes datos.",
}
SPECIAL_NEEDS_DIRECTIVE = "El cliente ha mencionado una necesidad especial: reconócela expresamente y ofrece anotarla en la reserva."

# ----------------------------------------------------------------- Jev -----


def jev_classify(api_key: str, state: str) -> dict[str, Any]:
    """One Jev System One call with three typed questions."""
    body = {
        "state": state,
        "model": "jev-latest",
        "questions": {
            "intent": {"type": "choice", "instructions": "Intención principal del ÚLTIMO mensaje del cliente a un restaurante por WhatsApp (el historial es solo contexto)", "criteria": INTENTS},
            "special_needs": {"type": "noul", "instructions": "El último mensaje menciona una necesidad especial: niños o bebés, tronas o carritos, movilidad reducida o silla de ruedas, alergia o intolerancia, embarazo, mascota o celebración"},
            "wants_note": {"type": "noul", "instructions": "El cliente pide anotar o apuntar algo (por ejemplo una alergia) en los comentarios de su reserva"},
            "frustration": {"type": "score", "instructions": "Nivel de frustración del cliente en el último mensaje", "criteria": ["Tranquilo", "Molesto pero educado", "Muy enfadado"]},
        },
    }
    req = urllib.request.Request(JEV_URL, json.dumps(body).encode(), {"Authorization": "Bearer " + api_key, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=6) as resp:
        answers = json.load(resp)["answers"]
    return {
        "intent": answers["intent"]["choice"],
        "confidence": float(answers["intent"].get("confidence", 0)),
        "probabilities": answers["intent"].get("probabilities", {}),
        "special_needs": float(answers["special_needs"]["noul"]),
        "wants_note": float(answers["wants_note"]["noul"]),
        "frustration": float(answers["frustration"]["score"]),
    }


class JevCategorizer(dspy.Module):
    """DSPy module wrapping Jev so it composes with the rest of the program."""

    def forward(self, api_key: str, state: str) -> dspy.Prediction:
        return dspy.Prediction(**jev_classify(api_key, state))


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
        recent = req.history[-6:]
        state = ("Historial:\n" + "\n".join(recent) + "\n\n" if recent else "") + "Último mensaje del cliente: " + req.text

        # jev_classify
        path.append("jev_classify")
        jev: dict[str, Any] = {}
        try:
            jev = self.jev(api_key=req.jev_api_key, state=state).toDict() if req.jev_api_key else {}
        except Exception as exc:  # noqa: BLE001 - never block the turn on Jev
            log.warning("jev_failed restaurant_id=%s err=%s", req.restaurant_id, exc)
        intent = jev.get("intent") or facts.get("regex_intent") or "other"
        confidence = float(jev.get("confidence", 0))
        classifier = "jev" if jev else "regex"

        # confident?
        path.append("confident")
        if confidence < 0.55:
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

        result: dict[str, Any] = {"intent": intent, "confidence": confidence, "classifier": classifier, "jev": jev}

        def done(action: str, node: str, routes: list[str], directive: str = "") -> dict[str, Any]:
            path.append(node)
            result.update(action=action, node=node, routes=routes, directive=directive, path=path)
            return result

        # same_day (facts computed in Go from the DB)
        path.append("same_day")
        if facts.get("same_day_intent"):
            return done("handoff_same_day", "handoff_same_day", [])

        # extras_change
        path.append("extras_change")
        if facts.get("regex_extras_mutation") or (intent == "extras" and confidence >= 0.7 and facts.get("mentions_extras")):
            return done("handoff_extras", "handoff_extras", [])

        # allergen_question: union of Jev and the deterministic regex, unless the
        # customer asks to write the allergy into the booking (agent can do it).
        path.append("allergen_question")
        wants_note = float(jev.get("wants_note", 0)) >= 0.6 or facts.get("regex_booking_note")
        if not wants_note and (facts.get("regex_allergen") or (intent == "allergens" and confidence >= 0.6)):
            return done("handoff_allergens", "handoff_allergens", [])

        # escalate
        path.append("escalate")
        if intent in ("human", "complaint") or float(jev.get("frustration", 0)) >= 1.5:
            return done("agent", "agent_human", ["human", "complaint"], DIRECTIVES["agent_human"])

        # special_needs
        path.append("special_needs")
        routes = list(ROUTE_TAGS.get(intent, []))
        extra = ""
        if float(jev.get("special_needs", 0)) >= 0.6 or wants_note:
            path.append("tag_special_needs")
            routes.append("special_needs")
            extra = " " + SPECIAL_NEEDS_DIRECTIVE

        # route_intent
        path.append("route_intent")
        node = {
            "create_booking": "agent_booking", "modify_booking": "agent_booking", "cancel_booking": "agent_booking",
            "rice": "agent_rice", "menu_policy": "agent_menu_policy", "availability": "agent_availability",
            "booking_status": "agent_status", "acknowledgement": "agent_short_reply",
        }.get(intent, "agent_general")
        return done("agent", node, routes, DIRECTIVES[node] + extra)


PIPELINE = BotPipeline()
app = FastAPI(title="wa-bot-pipeline")


@app.get("/healthz")
def healthz() -> dict[str, Any]:
    return {"ok": True, "dspy": dspy.__version__}


@app.get("/graph")
def graph() -> dict[str, Any]:
    return GRAPH


@app.post("/decide")
def decide(req: TurnRequest) -> dict[str, Any]:
    started = time.time()
    out = PIPELINE(req)
    out["elapsed_ms"] = int((time.time() - started) * 1000)
    log.info("checkpoint wa_bot_dspy_pipeline_v1 restaurant_id=%s session=%s intent=%s conf=%.2f node=%s ms=%s",
             req.restaurant_id, req.session_id, out["intent"], out["confidence"], out["node"], out["elapsed_ms"])
    return out
