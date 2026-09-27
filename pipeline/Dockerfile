# WhatsApp bot decision pipeline (DSPy + Jev) + voice-note transcription.
# Coordination ids: wa_bot_dspy_pipeline_v2, wa_bot_audio_transcription_v1
FROM python:3.12-slim
WORKDIR /app
ENV PYTHONUNBUFFERED=1 DSP_CACHEBOOL=false LITELLM_LOCAL_MODEL_COST_MAP=True LITELLM_LOG=ERROR HF_HOME=/opt/hf WHISPER_MODEL=small
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
# Bake the whisper model into the image: no download at runtime.
RUN python -c "from faster_whisper import WhisperModel; WhisperModel('small', device='cpu', compute_type='int8')"
COPY app.py .
# One worker: the whisper model is loaded once (~0.5 GB RAM); requests are
# served from the uvicorn threadpool.
CMD ["sh", "-c", "uvicorn app:app --host 127.0.0.1 --port ${BOT_PIPELINE_PORT:-18195} --workers 1"]
