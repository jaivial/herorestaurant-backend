# PaddleOCR Gateway

Local HTTP adapter for PaddleOCR-VL-1.6 stock document extraction.

## Install

Use separate virtual environment. PaddleOCR documents Python 3.9-3.13 support.

```bash
cd backend/ocr_gateway
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
```

For NVIDIA GPU deployments, install matching `paddlepaddle-gpu` package instead of `paddlepaddle`.

## Run

```bash
PADDLEOCR_MODEL=PaddleOCR-VL-1.6 \
python app.py
```

Service binds to `127.0.0.1:8090` by default. First extraction downloads model files unless they are already cached.

Configure Go backend:

```dotenv
STOCK_OCR_PROVIDER=paddleocr
PADDLEOCR_GATEWAY_URL=http://127.0.0.1:8090
PADDLEOCR_MODEL=PaddleOCR-VL-1.6
```

Gateway accepts `POST /extract` multipart fields `documentType`, `model`, and `file`. It accepts `INVOICE` and `RECIPE`, with a 10 MB limit. It never forwards uploaded documents to external services.

## Test

```bash
python -m unittest discover -s . -p 'test_*.py' -v
```
