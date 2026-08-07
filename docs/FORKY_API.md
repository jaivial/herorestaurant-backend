# Forky tools API

All tools execute against the authenticated active restaurant (`restaurant_id` is never accepted from model input). Responses are JSON strings and errors are returned by the assistant protocol.

| Tool | Scope | Permission | Side effects | Confirmation |
|---|---|---|---|---|
| `restaurant_info` | active restaurant | read:restaurant | none | no |
| `bookings_summary` | active restaurant | read:bookings | none | no |
| `restaurant_query` | active restaurant | read:analytics | none | no |
| `analytics_report` | active restaurant | read:analytics | none | no |
| `catalog_list`, `catalog_get` | active restaurant | read:catalog | none | no |
| `catalog_create`, `catalog_update`, `catalog_delete` | active restaurant | write:catalog | catalog mutation | server policy |
| `create_booking`, `update_booking`, `delete_booking` | active restaurant | write:bookings | booking mutation | `confirmed` plus server authorization |

## Booking schemas

`create_booking`: `{date:string,time:string,people:integer>=1,name:string,confirmed:boolean}`.
`update_booking`: `{booking_id:integer>=1,date?:string,time?:string,people?:integer,confirmed:boolean}`.
`delete_booking`: `{booking_id:integer>=1,confirmed:boolean}`.

All booking mutations enforce restaurant ownership in SQL (`WHERE restaurant_id=?`). Empty/invalid required values fail before mutation. Do not place credentials, session tokens, or payment data in tool arguments.

## Versioning

Current definitions are version 1. Clients must use the structured `tool_use` / `tool_result` protocol exposed by the assistant WebSocket.


## Read-only domain tools

| Tool | Scope | Limit | Side effects |
|---|---|---:|---|
| `schedules_list` | active restaurant | 100 | none |
| `customers_list` | active restaurant | 100 | none |
| `stock_items_list` | active restaurant | 100 | none |
| `pos_visits_list` | active restaurant | 100 | none |

Read tools reject arbitrary table/column names and always bind the active restaurant in SQL.

| `pos_visit_create` | active restaurant | write:pos | open visit | confirmation token |
| `pos_ticket_create` | active restaurant | write:pos | create ticket | confirmation token |
| `pos_payment_create` | active restaurant | write:pos | payment | confirmation token |
| `pos_refund_create` | active restaurant | write:pos | refund | confirmation token |
| `invoices_list`, `recipes_list`, `production_list`, `waste_costs_list` | active restaurant | read:domain | none | no |
| `restaurant_settings_get`, `whatsapp_bot_config_get`, `site_published_content_get` | active restaurant | read:settings | none | no |
