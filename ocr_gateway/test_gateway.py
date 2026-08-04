import unittest
from unittest.mock import patch

import app as gateway
from app import normalize_paddle_results


try:
    from fastapi.testclient import TestClient
except ImportError:
    TestClient = None


class PaddleOCRNormalizationTests(unittest.TestCase):
    def test_normalizes_invoice_markdown_into_stock_extraction(self):
        result = normalize_paddle_results(
            [
                {
                    "markdown": """
# Factura
Proveedor: Local Foods
Nº factura: F-42
Fecha: 30/07/2026

| Descripción | Cantidad | Unidad | Precio | Total |
| --- | ---: | --- | ---: | ---: |
| Harina | 2 | kg | 1,50 | 3,00 |
"""
                }
            ],
            "INVOICE",
        )

        self.assertEqual(result["supplierName"], "Local Foods")
        self.assertEqual(result["documentNumber"], "F-42")
        self.assertEqual(result["documentDate"], "2026-07-30")
        self.assertEqual(result["lines"][0]["description"], "Harina")
        self.assertEqual(result["lines"][0]["quantity"], 2.0)
        self.assertEqual(result["lines"][0]["unitPrice"], 1.5)
        self.assertEqual(result["lines"][0]["total"], 3.0)

    def test_normalizes_recipe_markdown_into_components(self):
        result = normalize_paddle_results(
            [
                {
                    "markdown": """
Nombre: Salsa verde
Rendimiento: 4 raciones

| Ingrediente | Cantidad | Unidad |
| --- | ---: | --- |
| Tomate | 500 | g |
| Aceite | 20 | ml |
"""
                }
            ],
            "RECIPE",
        )

        self.assertEqual(result["name"], "Salsa verde")
        self.assertEqual(result["yieldQuantity"], 4.0)
        self.assertEqual(result["yieldUnit"], "raciones")
        self.assertEqual(len(result["components"]), 2)
        self.assertEqual(result["components"][1]["description"], "Aceite")

    def test_ignores_non_document_payload_values(self):
        result = normalize_paddle_results(
            [{"input_path": "/tmp/secret.pdf", "page_index": 0, "markdown": "Texto"}],
            "INVOICE",
        )

        self.assertNotIn("/tmp/secret.pdf", result.get("rawText", ""))
        self.assertEqual(result["lines"], [])


@unittest.skipIf(TestClient is None or gateway.app is None, "FastAPI test dependencies unavailable")
class PaddleOCREndpointTests(unittest.TestCase):
    def test_extract_returns_provider_contract_without_external_call(self):
        with patch.object(gateway, "_run_pipeline", return_value=[{"markdown": "Proveedor: Test"}]) as run_pipeline:
            response = TestClient(gateway.app).post(
                "/extract",
                data={"documentType": "INVOICE", "model": "PaddleOCR-VL-1.6"},
                files={"file": ("invoice.png", b"png-payload", "image/png")},
            )

        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json()["model"], "PaddleOCR-VL-1.6")
        self.assertEqual(response.json()["extraction"]["supplierName"], "Test")
        run_pipeline.assert_called_once()

    def test_extract_rejects_unknown_document_type(self):
        response = TestClient(gateway.app).post(
            "/extract",
            data={"documentType": "UNKNOWN"},
            files={"file": ("invoice.png", b"png-payload", "image/png")},
        )

        self.assertEqual(response.status_code, 400)


if __name__ == "__main__":
    unittest.main()
