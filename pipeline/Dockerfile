# WhatsApp bot decision pipeline (DSPy). Coordination id: wa_bot_dspy_pipeline_v1
FROM python:3.12-slim
WORKDIR /app
ENV PYTHONUNBUFFERED=1 DSP_CACHEBOOL=false LITELLM_LOCAL_MODEL_COST_MAP=True LITELLM_LOG=ERROR
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app.py .
CMD ["sh", "-c", "uvicorn app:app --host 127.0.0.1 --port ${BOT_PIPELINE_PORT:-18195} --workers 2"]
