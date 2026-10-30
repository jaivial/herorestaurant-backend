# Laya heads for the WhatsApp bot pipeline (Jev /v1/systemone protocol).
# Coordination id: wa_bot_laya_heads_v1
FROM python:3.12-slim AS base
WORKDIR /app
ENV PYTHONUNBUFFERED=1 HF_HOME=/opt/hf
RUN pip install --no-cache-dir --index-url https://download.pytorch.org/whl/cpu torch==2.14.1
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
# Bake the pinned multilingual Laya checkpoint: no download at runtime.
RUN python -c "from huggingface_hub import snapshot_download as d; d('convaiinnovations/laya', revision='7b928d828b7b0e022f929d9bd2e44165aa270148', allow_patterns=['multilingual/*'])"
ENV HF_HUB_OFFLINE=1

# Training runs on `base` + scikit-learn (train_heads.py); serving only needs numpy.
FROM base
COPY heads.py server.py train_heads.py heads.npz heads.json ./
CMD ["sh", "-c", "uvicorn server:app --host 127.0.0.1 --port ${LAYA_HEADS_PORT:-18196} --workers 1"]
