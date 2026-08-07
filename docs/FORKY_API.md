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
