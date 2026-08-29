# Public restaurant ads

`GET /api/public/ads`

Query parameters:

- `restaurant_id` (optional): positive restaurant ID. If omitted, the backend resolves the restaurant from the request tenant/host.
- `date` (optional): `YYYY-MM-DD`. If provided, only active ads whose inclusive schedule contains that date are returned. Active ads without a schedule are always eligible.

Response:

```json
{
  "success": true,
  "restaurant_id": 1,
  "ads": [
    {
      "id": 12,
      "name": "Anuncio",
      "active": true,
      "starts_at": "2026-08-29",
      "ends_at": "2026-08-31",
      "content": [],
      "ctas": []
    }
  ]
}
```
