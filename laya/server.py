"""Laya heads server: the Jev /v1/systemone protocol for the WhatsApp bot pipeline.

Coordination id: wa_bot_laya_heads_v1

Questions with a trained head (intent, booking_op, stage, language, anger,
urgency and the noul meters) are answered from one encoder pass over the last
customer message (+ the previous line). Dynamic questions (same_topic,
same_request, special_date) are left unanswered: the pipeline keeps its own
fallback for them.
"""
import logging
import os
import time

import torch
from fastapi import FastAPI

from heads import Heads, featurize, load_encoder, split_state

torch.set_num_threads(int(os.environ.get("LAYA_HEADS_THREADS", "2")))
log = logging.getLogger("laya_heads")
logging.basicConfig(level=logging.INFO, format="%(asctime)s [laya_heads] %(message)s")

HEADS = Heads("heads.npz", "heads.json")
EMBED = load_encoder()
app = FastAPI(title="laya-villacarmen-heads")


@app.get("/health")
def health():
    return {"status": "ok", "heads": sorted(HEADS.meta["heads"]), "trained_at": HEADS.meta["trained_at"], "revision": HEADS.meta["revision"]}


@app.post("/v1/systemone")
def systemone(body: dict):
    t = time.perf_counter()
    questions = body.get("questions") or {}
    last, msg = split_state(body.get("state", ""))
    views = featurize(EMBED, [(last, msg)]) if HEADS.views_needed(questions) else {}
    answers = {}
    for qid, q in questions.items():
        if qid in HEADS.meta["heads"]:
            a = HEADS.answer(qid, q, views[HEADS.meta["heads"][qid]["view"]])
            if a is not None:
                answers[qid] = a
    ms = (time.perf_counter() - t) * 1000
    intent = answers.get("intent", {})
    # Coordination id: wa_bot_laya_heads_v1 - one line per decision: latency and the intent.
    log.info("checkpoint wa_bot_laya_heads_v1 ms=%.0f answered=%d/%d intent=%s conf=%s", ms, len(answers), len(questions),
             intent.get("choice"), intent.get("confidence"))
    return {"model": "laya-villacarmen-heads", "answers": answers, "usage": {"input_tokens": 0, "output_tokens": 0, "latency_ms": round(ms)}}
