"""Train the Laya heads (wa_bot_laya_heads_v1). Runs in the laya image + scikit-learn.

usage: python train_heads.py <data_dir> <out_dir>

data_dir (never committed: real customer messages):
  dataset.json   turns decided by Jev in production, split dev/test
  corpus.json    real customer turns (current + old bot), test messages removed
  synth_rows.json  LLM-generated turns per intent
  t_flash_*.jsonl / tx_*.jsonl  teacher labels (MiniMax) for every field
out_dir: heads.npz + heads.json (weights + metadata + held-out metrics).
"""
import json
import sys
import time

import numpy as np
from sklearn.linear_model import LogisticRegression
from sklearn.model_selection import cross_val_score
from sklearn.pipeline import make_pipeline
from sklearn.preprocessing import StandardScaler

from heads import REVISION, featurize, load_encoder

DATA, OUT = sys.argv[1], sys.argv[2]
CHOICE = ["intent", "booking_op", "stage", "language"]
SCORE = ["anger", "urgency"]
NOUL = ["can_handle", "event", "special_needs", "wants_note", "has_date", "has_time", "has_people", "multi_request", "injection", "off_topic", "formal"]
# Thresholds the pipeline applies to each meter (pipeline/app.py): FP/TP are reported there.
THRESHOLDS = {"can_handle": ("<", 0.30), "event": (">=", 0.75), "special_needs": (">=", 0.6), "wants_note": (">=", 0.6), "has_date": (">=", 0.5),
              "has_time": (">=", 0.5), "has_people": (">=", 0.5), "multi_request": (">=", 0.7), "injection": (">=", 0.8), "off_topic": (">=", 0.7),
              "formal": (">=", 0.7), "anger": (">=", 1.6), "urgency": (">=", 1.6)}


def jl(name):
    out = {}
    for line in open(f"{DATA}/{name}"):
        try:
            x = json.loads(line)
            out[x["key"]] = x["label"]
        except ValueError:
            pass
    return out


def labels(*maps):
    keys = set().union(*maps)
    return {k: {kk: vv for m in maps for kk, vv in m.get(k, {}).items()} for k in keys}


ds = json.load(open(f"{DATA}/dataset.json"))
corpus = json.load(open(f"{DATA}/corpus.json"))
synth = json.load(open(f"{DATA}/synth_rows.json"))
L = labels(jl("t_flash_ds.jsonl"), jl("tx_ds.jsonl"), jl("t_flash_corpus.jsonl"), jl("tx_corpus.jsonl"), jl("t_flash_synth.jsonl"), jl("tx_synth.jsonl"))

train = [dict(r, y=L[r["uid"]]) for r in corpus if r["uid"] in L]
train += [dict(r, y=L[str(r["id"])]) for r in ds if r["split"] == "dev" and str(r["id"]) in L]
# Synthetic turns only where the teacher agrees with the intent they were generated for.
syn = [dict(r, y=L[r["uid"]]) for r in synth if r["uid"] in L and L[r["uid"]].get("intent") == r["intent"]]
test = [dict(r, y=L[str(r["id"])]) for r in ds if r["split"] == "test" and str(r["id"]) in L]
print(f"train real={len(train)} synth_kept={len(syn)}/{len(synth)} test={len(test)}", flush=True)
train += syn

t0 = time.time()
embed = load_encoder()
pair = lambda r: (r["history"][-1] if r["history"] else "", r["message"])
Xtr, Xte = featurize(embed, [pair(r) for r in train]), featurize(embed, [pair(r) for r in test])
print(f"embedded {len(train) + len(test)} turns x2 views in {time.time() - t0:.0f}s", flush=True)


def target(field, y):
    v = y.get(field)
    if v is None:
        return None
    if field in NOUL:
        return str(bool(v))
    if field in SCORE:
        return str(int(v))
    return str(v)


def fires(field, p, classes):
    """Did the pipeline threshold fire, for each row?"""
    op, th = THRESHOLDS[field]
    if field in SCORE:
        val = p @ np.array([int(c) for c in classes], dtype=float)
    else:
        val = p[:, classes.index("True")]
    return val < th if op == "<" else val >= th


params, meta = {}, {"revision": REVISION, "trained_at": time.strftime("%Y-%m-%d %H:%M:%S"), "n_train": len(train), "n_test": len(test), "heads": {}}
for field in CHOICE + SCORE + NOUL:
    idx = [i for i, r in enumerate(train) if target(field, r["y"]) is not None]
    y = np.array([target(field, train[i]["y"]) for i in idx])
    if len(set(y)) < 2:
        print(f"{field:<14} skipped (one class)")
        continue
    C = 0.05 if field in CHOICE else 0.1
    # The view is chosen on REAL turns only: synthetic turns carry a context
    # line that favours the 'both' view, which then loses on real traffic.
    real = [i for i in idx if train[i].get("src") != "syn"]
    yr = np.array([target(field, train[i]["y"]) for i in real])
    best = "msg" if min(np.unique(yr, return_counts=True)[1].min(), len(set(yr))) < 2 else \
        max(("msg", "both"), key=lambda v: cross_val_score(make_pipeline(StandardScaler(), LogisticRegression(C=C, max_iter=3000)),
                                                           Xtr[v][real], yr, cv=5).mean())
    sc = StandardScaler().fit(Xtr[best][idx])
    lr = LogisticRegression(C=C, max_iter=3000).fit(sc.transform(Xtr[best][idx]), y)
    classes = [str(c) for c in lr.classes_]
    params.update({f"{field}.W": lr.coef_.astype(np.float32), f"{field}.b": lr.intercept_.astype(np.float32),
                   f"{field}.mean": sc.mean_.astype(np.float32), f"{field}.scale": sc.scale_.astype(np.float32)})
    kind = "choice" if field in CHOICE else "score" if field in SCORE else "noul"
    p = lr.predict_proba(sc.transform(Xte[best]))
    gold = np.array([target(field, r["y"]) for r in test])
    m = {"view": best, "acc": round(float(np.mean(np.array(classes)[p.argmax(1)] == gold)), 3), "positives_train": int(np.sum(y != y[0])) if kind == "noul" else None}
    if field == "intent":
        conf, pred = p.max(1), np.array(classes)[p.argmax(1)]
        jev = np.array([r["jev"]["intent"] for r in test])
        hi = np.array([r["jev"]["confidence"] >= 0.7 for r in test])
        m["acc_vs_jev"] = round(float(np.mean(pred[hi] == jev[hi])), 3)
        m["gate"] = {str(th): {"coverage": round(float((conf >= th).mean()), 3), "acc": round(float(np.mean(pred[conf >= th] == gold[conf >= th])), 3)}
                     for th in (0.55, 0.6, 0.7, 0.8)}
    if field in THRESHOLDS:
        f = fires(field, p, classes)
        g = np.array([(gold_v == "False") if field == "can_handle" else (int(gold_v) >= 2 if field in SCORE else gold_v == "True") for gold_v in gold])
        m["fires"] = {"false_alarms": f"{int(np.sum(f & ~g))}/{int(np.sum(~g))}", "hits": f"{int(np.sum(f & g))}/{int(np.sum(g))}"}
    meta["heads"][field] = {"kind": kind, "view": best, "classes": classes, "metrics": m}
    print(f"{field:<14} {json.dumps(m, ensure_ascii=False)}", flush=True)

np.savez_compressed(f"{OUT}/heads.npz", **params)
json.dump(meta, open(f"{OUT}/heads.json", "w"), ensure_ascii=False, indent=1)
print(f"saved {OUT}/heads.npz ({len(meta['heads'])} heads) in {time.time() - t0:.0f}s")
