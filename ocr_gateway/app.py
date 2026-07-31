import json
import os
import re
import tempfile
from datetime import datetime
from functools import lru_cache
from pathlib import Path

try:
    from fastapi import FastAPI, File, Form, HTTPException, UploadFile
except ImportError:  # Keep pure normalization tests runnable without service deps.
    FastAPI = None
    File = Form = UploadFile = None
    HTTPException = RuntimeError


MAX_DOCUMENT_BYTES = 10 * 1024 * 1024
DEFAULT_MODEL = os.getenv("PADDLEOCR_MODEL", "PaddleOCR-VL-1.6")


def _payload_from_result(result):
    if isinstance(result, (dict, list)):
        return result
    for attribute in ("json", "res"):
        value = getattr(result, attribute, None)
        if callable(value):
            value = value()
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except json.JSONDecodeError:
                continue
        if isinstance(value, (dict, list)):
            return value
    return {}


def _collect_text(value, output):
    if isinstance(value, dict):
        for key, child in value.items():
            if key in {"markdown", "block_content", "text", "content"} and isinstance(child, str):
                output.append(child)
            elif key not in {"input_path", "page_index", "model_settings"}:
                _collect_text(child, output)
    elif isinstance(value, list):
        for child in value:
            _collect_text(child, output)


def _document_text(results):
    chunks = []
    for result in results:
        _collect_text(_payload_from_result(result), chunks)
    return "\n".join(dict.fromkeys(chunk.strip() for chunk in chunks if chunk.strip()))


def _number(value):
    cleaned = re.sub(r"[^0-9,.-]", "", value.strip())
    if not cleaned:
        return 0.0
    if "," in cleaned and "." in cleaned:
        if cleaned.rfind(",") > cleaned.rfind("."):
            cleaned = cleaned.replace(".", "").replace(",", ".")
        else:
            cleaned = cleaned.replace(",", "")
    elif "," in cleaned:
        cleaned = cleaned.replace(",", ".")
    try:
        return float(cleaned)
    except ValueError:
        return 0.0


def _label_value(text, labels):
    label_pattern = "|".join(re.escape(label) for label in labels)
    match = re.search(rf"(?:{label_pattern})[ \t]*[:#-]?[ \t]*([^\n|]+)", text, re.IGNORECASE)
    return match.group(1).strip() if match else ""


def _date_value(text):
    match = re.search(r"\b(\d{1,2})[/-](\d{1,2})[/-](\d{2,4})\b", text)
    if match:
        day, month, year = match.groups()
        if len(year) == 2:
            year = "20" + year
        try:
            return datetime(int(year), int(month), int(day)).strftime("%Y-%m-%d")
        except ValueError:
            return ""
    match = re.search(r"\b(\d{4})-(\d{2})-(\d{2})\b", text)
    if match:
        try:
            return datetime.strptime(match.group(0), "%Y-%m-%d").strftime("%Y-%m-%d")
        except ValueError:
            return ""
    return ""


def _table_rows(text):
    rows = []
    for line in text.splitlines():
        line = line.strip()
        if not line.startswith("|") or not line.endswith("|"):
            continue
        cells = [cell.strip() for cell in line.strip("|").split("|")]
        if cells and not all(re.fullmatch(r":?-+:?", cell) for cell in cells):
            rows.append(cells)
    return rows


def _header_index(headers, words):
    for index, header in enumerate(headers):
        normalized = header.lower()
        if any(word in normalized for word in words):
            return index
    return None


def _extract_table_lines(text, recipe):
    lines = []
    for rows in [_table_rows(text)]:
        if len(rows) < 2:
            continue
        headers = rows[0]
        description_index = _header_index(headers, ["descrip", "producto", "artículo", "articulo", "ingrediente", "concepto"])
        quantity_index = _header_index(headers, ["cantidad", "qty", "cant"])
        unit_index = _header_index(headers, ["unidad", "unit"])
        unit_price_index = _header_index(headers, ["precio", "price", "p.u", "unitario"])
        total_index = _header_index(headers, ["total", "importe"])
        if description_index is None:
            continue
        for row in rows[1:]:
            if len(row) <= description_index or not row[description_index].strip():
                continue
            def cell(index):
                return row[index] if index is not None and index < len(row) else ""

            item = {
                "description": cell(description_index),
                "code": "",
                "quantity": _number(cell(quantity_index)),
                "unit": cell(unit_index),
                "unitPrice": _number(cell(unit_price_index)),
                "total": _number(cell(total_index)),
                "wastePct": 0.0,
                "confidence": 0.0,
            }
            if item["description"].lower() in {"subtotal", "iva", "iva", "total", "base imponible"}:
                continue
            item["confidence"] = 0.85 if recipe or total_index is not None else 0.7
            lines.append(item)
    return lines


def normalize_paddle_results(results, document_type):
    text = _document_text(results)
    is_recipe = document_type == "RECIPE"
    extraction = {
        "supplierName": "" if is_recipe else _label_value(text, ["proveedor", "supplier", "empresa"]),
        "documentNumber": "" if is_recipe else _label_value(text, ["nº factura", "no. factura", "número de factura", "numero de factura", "factura"]),
        "documentDate": "" if is_recipe else _date_value(text),
        "name": _label_value(text, ["nombre", "receta", "name"]) if is_recipe else "",
        "yieldQuantity": 0.0,
        "yieldUnit": "",
        "confidence": 0.0,
        "lines": [] if is_recipe else _extract_table_lines(text, False),
        "components": _extract_table_lines(text, True) if is_recipe else [],
        "rawText": text,
    }
    if is_recipe:
        yield_value = _label_value(text, ["rendimiento", "yield", "raciones", "porciones"])
        match = re.search(r"([0-9]+(?:[,.][0-9]+)?)\s*([^\n,]+)?", yield_value)
        if match:
            extraction["yieldQuantity"] = _number(match.group(1))
            extraction["yieldUnit"] = (match.group(2) or "").strip()
    extracted_lines = extraction["components"] if is_recipe else extraction["lines"]
    extraction["confidence"] = 0.85 if extracted_lines else (0.45 if text else 0.0)
    return extraction


@lru_cache(maxsize=1)
def _pipeline(model):
    try:
        from paddleocr import PaddleOCRVL
    except ImportError as error:
        raise RuntimeError("PaddleOCR is not installed") from error
    try:
        return PaddleOCRVL(
            pipeline_version="v1.6",
            vl_rec_model_name=model,
            format_block_content=True,
        )
    except TypeError:
        return PaddleOCRVL(pipeline_version="v1.6")


def _run_pipeline(path, model):
    result = _pipeline(model).predict(input=path)
    return list(result)


if FastAPI is not None:
    app = FastAPI(title="Villacarmen PaddleOCR Gateway")

    @app.get("/healthz")
    def healthz():
        return {"success": True, "provider": "paddleocr", "model": DEFAULT_MODEL}

    @app.post("/extract")
    async def extract(documentType: str = Form(...), model: str = Form(DEFAULT_MODEL), file: UploadFile = File(...)):
        if documentType not in {"INVOICE", "RECIPE"}:
            raise HTTPException(status_code=400, detail="Invalid document type")
        payload = await file.read(MAX_DOCUMENT_BYTES + 1)
        if not payload or len(payload) > MAX_DOCUMENT_BYTES:
            raise HTTPException(status_code=413, detail="Document exceeds 10 MB")
        suffix = Path(file.filename or "document").suffix or ".bin"
        selected_model = model.strip() or DEFAULT_MODEL
        temporary_path = None
        try:
            with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as temporary:
                temporary.write(payload)
                temporary_path = temporary.name
            normalized = normalize_paddle_results(_run_pipeline(temporary_path, selected_model), documentType)
            raw_text = normalized.pop("rawText", "")
            return {"success": True, "model": selected_model, "rawText": raw_text, "extraction": normalized}
        except Exception as error:
            raise HTTPException(status_code=502, detail=str(error)) from error
        finally:
            if temporary_path:
                try:
                    os.unlink(temporary_path)
                except OSError:
                    pass
else:
    app = None


if __name__ == "__main__":
    if app is None:
        raise SystemExit("Install gateway dependencies before starting service")
    import uvicorn

    uvicorn.run(app, host=os.getenv("PADDLEOCR_GATEWAY_HOST", "127.0.0.1"), port=int(os.getenv("PADDLEOCR_GATEWAY_PORT", "8090")))
