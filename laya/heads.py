"""Laya encoder embeddings + trained linear heads, shared by training and serving.

Coordination id: wa_bot_laya_heads_v1

The heads read the frozen multilingual Laya encoder (mean-pooled embeddings) and
answer the bot pipeline's typed questions in the Jev /v1/systemone shape. Only
numpy runs at serving time on top of the encoder.
"""
import json

import numpy as np

REPO = "convaiinnovations/laya"
SUBFOLDER = "multilingual"
REVISION = "7b928d828b7b0e022f929d9bd2e44165aa270148"
MARK = "Último mensaje del cliente:"


def load_encoder():
    import laya

    agent = laya.load(REPO, subfolder=SUBFOLDER, revision=REVISION, device="cpu")
    return laya.embed_fn_from_agent(agent, max_length=512, batch_size=16)


def split_state(state):
    """(last history line, last customer message) from the pipeline state text."""
    head, found, msg = str(state).rpartition(MARK)
    if not found:
        return "", str(state).strip()
    lines = [l.strip() for l in head.splitlines() if l.strip() and l.strip() != "Historial:"]
    return (lines[-1] if lines else ""), msg.strip()


def _unit(x):
    return x / np.maximum(np.linalg.norm(x, axis=1, keepdims=True), 1e-12)


def featurize(embed, pairs):
    """Views of (last, message) pairs: 'msg' and 'both' (message + context)."""
    m = _unit(np.asarray(embed([msg for _, msg in pairs]), dtype=np.float32))
    c = _unit(np.asarray(embed([(last + "\n" if last else "") + "Cliente: " + msg for last, msg in pairs]), dtype=np.float32))
    return {"msg": m, "both": np.hstack([m, c])}


class Heads:
    """Linear heads exported by train_heads.py (heads.npz + heads.json)."""

    def __init__(self, npz_path, meta_path):
        z = np.load(npz_path)
        self.meta = json.load(open(meta_path))
        self.p = {k: z[k] for k in z.files}

    def views_needed(self, qids):
        return {self.meta["heads"][q]["view"] for q in qids if q in self.meta["heads"]}

    def proba(self, name, X):
        h = self.meta["heads"][name]
        z = (X - self.p[name + ".mean"]) / self.p[name + ".scale"]
        logits = z @ self.p[name + ".W"].T + self.p[name + ".b"]
        if logits.shape[1] == 1:  # binary logistic regression
            p1 = 1 / (1 + np.exp(-logits[:, 0]))
            return np.stack([1 - p1, p1], axis=1), h["classes"]
        e = np.exp(logits - logits.max(axis=1, keepdims=True))
        return e / e.sum(axis=1, keepdims=True), h["classes"]

    def answer(self, name, question, X):
        """One Jev-shaped answer, or None when the question does not match the head."""
        h = self.meta["heads"].get(name)
        if not h or question.get("type") != h["kind"]:
            return None
        p, classes = self.proba(name, X)
        p = p[0]
        if h["kind"] == "noul":
            return {"type": "noul", "noul": round(float(p[classes.index("True")]), 4)}
        conf = round(float(p.max()), 4)
        if h["kind"] == "score":
            levels = [int(c) for c in classes]
            return {"type": "score", "score": round(float(sum(l * q for l, q in zip(levels, p))), 4),
                    "probabilities": {str(l): round(float(q), 4) for l, q in zip(levels, p)}, "confidence": conf, "x_jev_confidence": conf}
        crit = question.get("criteria")
        keys = list(crit) if isinstance(crit, (dict, list)) else []
        if not set(classes) <= set(keys):
            return None  # the pipeline changed the labels: let it fall back
        probs = {k: 0.0 for k in keys}
        probs.update({c: round(float(q), 4) for c, q in zip(classes, p)})
        return {"type": "choice", "choice": classes[int(p.argmax())], "probabilities": probs, "confidence": conf, "x_jev_confidence": conf}
