# User stories - Backoffice Alqueria (paso 7)

Generado desde ENDPOINTS.md (fuente de verdad de la API). Cada historia tiene criterio de aceptacion testeable con cu contra backoffice-dev.menustudioai.com.

Estados: pendiente / ok / bug (con PR que lo arregla).

| ID | Area | Historia | Criterio de aceptacion | Estado |
|----|------|----------|------------------------|--------|
| US-0001 | Publico | Como visitante puedo consultar booking?id=<booking_id> (`GET /api/public/booking?id=<booking_id>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0002 | Backoffice | Como miembro del backoffice puedo consultar overview (`GET /api/admin/analytics/overview`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0003 | Backoffice | Sin sesion valida, `GET /api/admin/analytics/overview` | Respuesta 401/403, nunca datos | pendiente |
| US-0004 | Backoffice | Como miembro del backoffice puedo ejecutar refresh (`POST /api/admin/analytics/refresh`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0005 | Backoffice | Sin sesion valida, `POST /api/admin/analytics/refresh` | Respuesta 401/403, nunca datos | pendiente |
| US-0006 | Backoffice | `POST /api/admin/analytics/refresh` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0007 | Backoffice | Como miembro del backoffice puedo ejecutar login (`POST /api/admin/login`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0008 | Backoffice | `POST /api/admin/login` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0009 | Backoffice | Como miembro del backoffice puedo ejecutar logout (`POST /api/admin/logout`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0010 | Backoffice | Sin sesion valida, `POST /api/admin/logout` | Respuesta 401/403, nunca datos | pendiente |
| US-0011 | Backoffice | `POST /api/admin/logout` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0012 | Backoffice | Como miembro del backoffice puedo consultar me (`GET /api/admin/me`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0013 | Backoffice | Sin sesion valida, `GET /api/admin/me` | Respuesta 401/403, nunca datos | pendiente |
| US-0014 | Backoffice | Como miembro del backoffice puedo actualizar preferences (`PUT /api/admin/me/preferences`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0015 | Backoffice | Sin sesion valida, `PUT /api/admin/me/preferences` | Respuesta 401/403, nunca datos | pendiente |
| US-0016 | Backoffice | `PUT /api/admin/me/preferences` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0017 | API | Como cliente de la API puedo consultar healthz (`GET /healthz`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0018 | Backoffice | Como miembro del backoffice puedo consultar metrics (`GET /api/admin/dashboard/metrics`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0019 | Backoffice | Sin sesion valida, `GET /api/admin/dashboard/metrics` | Respuesta 401/403, nunca datos | pendiente |
| US-0020 | Backoffice | Como miembro del backoffice puedo ejecutar password (`POST /api/admin/me/password`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0021 | Backoffice | Sin sesion valida, `POST /api/admin/me/password` | Respuesta 401/403, nunca datos | pendiente |
| US-0022 | Backoffice | `POST /api/admin/me/password` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0023 | Backoffice | Como miembro del backoffice puedo ejecutar active restaurant (`POST /api/admin/active-restaurant`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0024 | Backoffice | Sin sesion valida, `POST /api/admin/active-restaurant` | Respuesta 401/403, nunca datos | pendiente |
| US-0025 | Backoffice | `POST /api/admin/active-restaurant` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0026 | Backoffice | Como miembro del backoffice puedo consultar members (`GET /api/admin/members`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0027 | Backoffice | Sin sesion valida, `GET /api/admin/members` | Respuesta 401/403, nunca datos | pendiente |
| US-0028 | Backoffice | Como miembro del backoffice puedo ejecutar members (`POST /api/admin/members`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0029 | Backoffice | Sin sesion valida, `POST /api/admin/members` | Respuesta 401/403, nunca datos | pendiente |
| US-0030 | Backoffice | `POST /api/admin/members` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0031 | Backoffice | Como miembro del backoffice puedo ejecutar resend (`POST /api/admin/members/{id}/invitation/resend`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0032 | Backoffice | Sin sesion valida, `POST /api/admin/members/{id}/invitation/resend` | Respuesta 401/403, nunca datos | pendiente |
| US-0033 | Backoffice | `POST /api/admin/members/{id}/invitation/resend` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0034 | Backoffice | Como miembro del backoffice puedo ejecutar send (`POST /api/admin/members/{id}/password-reset/send`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0035 | Backoffice | Sin sesion valida, `POST /api/admin/members/{id}/password-reset/send` | Respuesta 401/403, nunca datos | pendiente |
| US-0036 | Backoffice | `POST /api/admin/members/{id}/password-reset/send` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0037 | Backoffice | Como miembro del backoffice puedo consultar elemento (`GET /api/admin/members/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0038 | Backoffice | Sin sesion valida, `GET /api/admin/members/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0039 | Backoffice | Como miembro del backoffice puedo actualizar elemento (`PATCH /api/admin/members/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0040 | Backoffice | Sin sesion valida, `PATCH /api/admin/members/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0041 | Backoffice | `PATCH /api/admin/members/{id}` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0042 | Backoffice | Como miembro del backoffice puedo eliminar elemento (`DELETE /api/admin/members/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0043 | Backoffice | Sin sesion valida, `DELETE /api/admin/members/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0044 | Backoffice | Como miembro del backoffice puedo consultar compensations (`GET /api/admin/members/{id}/compensations`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0045 | Backoffice | Sin sesion valida, `GET /api/admin/members/{id}/compensations` | Respuesta 401/403, nunca datos | pendiente |
| US-0046 | Backoffice | Como miembro del backoffice puedo ejecutar compensations (`POST /api/admin/members/{id}/compensations`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0047 | Backoffice | Sin sesion valida, `POST /api/admin/members/{id}/compensations` | Respuesta 401/403, nunca datos | pendiente |
| US-0048 | Backoffice | `POST /api/admin/members/{id}/compensations` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0049 | Backoffice | Como miembro del backoffice puedo actualizar elemento (`PATCH /api/admin/members/{id}/compensations/{compensationId}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0050 | Backoffice | Sin sesion valida, `PATCH /api/admin/members/{id}/compensations/{compensationId}` | Respuesta 401/403, nunca datos | pendiente |
| US-0051 | Backoffice | `PATCH /api/admin/members/{id}/compensations/{compensationId}` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0052 | Backoffice | Como miembro del backoffice puedo eliminar elemento (`DELETE /api/admin/members/{id}/compensations/{compensationId}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0053 | Backoffice | Sin sesion valida, `DELETE /api/admin/members/{id}/compensations/{compensationId}` | Respuesta 401/403, nunca datos | pendiente |
| US-0054 | Backoffice | Como miembro del backoffice puedo consultar stats (`GET /api/admin/members/{id}/stats`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0055 | Backoffice | Sin sesion valida, `GET /api/admin/members/{id}/stats` | Respuesta 401/403, nunca datos | pendiente |
| US-0056 | Backoffice | Como miembro del backoffice puedo consultar time balance (`GET /api/admin/members/{id}/time-balance`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0057 | Backoffice | Sin sesion valida, `GET /api/admin/members/{id}/time-balance` | Respuesta 401/403, nunca datos | pendiente |
| US-0058 | Backoffice | Como miembro del backoffice puedo ejecutar ensure user (`POST /api/admin/members/{id}/ensure-user`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0059 | Backoffice | Sin sesion valida, `POST /api/admin/members/{id}/ensure-user` | Respuesta 401/403, nunca datos | pendiente |
| US-0060 | Backoffice | `POST /api/admin/members/{id}/ensure-user` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0061 | Backoffice | Como miembro del backoffice puedo consultar roles (`GET /api/admin/roles`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0062 | Backoffice | Sin sesion valida, `GET /api/admin/roles` | Respuesta 401/403, nunca datos | pendiente |
| US-0063 | Backoffice | Como miembro del backoffice puedo ejecutar roles (`POST /api/admin/roles`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0064 | Backoffice | Sin sesion valida, `POST /api/admin/roles` | Respuesta 401/403, nunca datos | pendiente |
| US-0065 | Backoffice | `POST /api/admin/roles` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0066 | Backoffice | Como miembro del backoffice puedo actualizar role (`PATCH /api/admin/users/{id}/role`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0067 | Backoffice | Sin sesion valida, `PATCH /api/admin/users/{id}/role` | Respuesta 401/403, nunca datos | pendiente |
| US-0068 | Backoffice | `PATCH /api/admin/users/{id}/role` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0069 | Backoffice | Como miembro del backoffice puedo ejecutar validate (`POST /api/admin/invitations/validate`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0070 | Backoffice | Sin sesion valida, `POST /api/admin/invitations/validate` | Respuesta 401/403, nunca datos | pendiente |
| US-0071 | Backoffice | `POST /api/admin/invitations/validate` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0072 | Backoffice | Como miembro del backoffice puedo ejecutar start (`POST /api/admin/invitations/onboarding/start`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0073 | Backoffice | Sin sesion valida, `POST /api/admin/invitations/onboarding/start` | Respuesta 401/403, nunca datos | pendiente |
| US-0074 | Backoffice | `POST /api/admin/invitations/onboarding/start` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0075 | Backoffice | Como miembro del backoffice puedo consultar elemento (`GET /api/admin/invitations/onboarding/{guid}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0076 | Backoffice | Sin sesion valida, `GET /api/admin/invitations/onboarding/{guid}` | Respuesta 401/403, nunca datos | pendiente |
| US-0077 | Backoffice | Como miembro del backoffice puedo ejecutar profile (`POST /api/admin/invitations/onboarding/{guid}/profile`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0078 | Backoffice | Sin sesion valida, `POST /api/admin/invitations/onboarding/{guid}/profile` | Respuesta 401/403, nunca datos | pendiente |
| US-0079 | Backoffice | `POST /api/admin/invitations/onboarding/{guid}/profile` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0080 | Backoffice | Como miembro del backoffice puedo ejecutar avatar (`POST /api/admin/invitations/onboarding/{guid}/avatar`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0081 | Backoffice | Sin sesion valida, `POST /api/admin/invitations/onboarding/{guid}/avatar` | Respuesta 401/403, nunca datos | pendiente |
| US-0082 | Backoffice | `POST /api/admin/invitations/onboarding/{guid}/avatar` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0083 | Backoffice | Como miembro del backoffice puedo ejecutar password (`POST /api/admin/invitations/onboarding/{guid}/password`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0084 | Backoffice | Sin sesion valida, `POST /api/admin/invitations/onboarding/{guid}/password` | Respuesta 401/403, nunca datos | pendiente |
| US-0085 | Backoffice | `POST /api/admin/invitations/onboarding/{guid}/password` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0086 | Backoffice | Como miembro del backoffice puedo ejecutar validate (`POST /api/admin/password-resets/validate`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0087 | Backoffice | Sin sesion valida, `POST /api/admin/password-resets/validate` | Respuesta 401/403, nunca datos | pendiente |
| US-0088 | Backoffice | `POST /api/admin/password-resets/validate` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0089 | Backoffice | Como miembro del backoffice puedo ejecutar confirm (`POST /api/admin/password-resets/confirm`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0090 | Backoffice | Sin sesion valida, `POST /api/admin/password-resets/confirm` | Respuesta 401/403, nunca datos | pendiente |
| US-0091 | Backoffice | `POST /api/admin/password-resets/confirm` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0092 | Backoffice | Como miembro del backoffice puedo consultar ping (`GET /api/admin/fichaje/ping`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0093 | Backoffice | Sin sesion valida, `GET /api/admin/fichaje/ping` | Respuesta 401/403, nunca datos | pendiente |
| US-0094 | Backoffice | Como miembro del backoffice puedo consultar state (`GET /api/admin/fichaje/state`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0095 | Backoffice | Sin sesion valida, `GET /api/admin/fichaje/state` | Respuesta 401/403, nunca datos | pendiente |
| US-0096 | Backoffice | Como miembro del backoffice puedo ejecutar start (`POST /api/admin/fichaje/start`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0097 | Backoffice | Sin sesion valida, `POST /api/admin/fichaje/start` | Respuesta 401/403, nunca datos | pendiente |
| US-0098 | Backoffice | `POST /api/admin/fichaje/start` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0099 | Backoffice | Como miembro del backoffice puedo ejecutar stop (`POST /api/admin/fichaje/stop`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0100 | Backoffice | Sin sesion valida, `POST /api/admin/fichaje/stop` | Respuesta 401/403, nunca datos | pendiente |
| US-0101 | Backoffice | `POST /api/admin/fichaje/stop` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0102 | Backoffice | Como miembro del backoffice puedo ejecutar start (`POST /api/admin/fichaje/admin/start`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0103 | Backoffice | Sin sesion valida, `POST /api/admin/fichaje/admin/start` | Respuesta 401/403, nunca datos | pendiente |
| US-0104 | Backoffice | `POST /api/admin/fichaje/admin/start` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0105 | Backoffice | Como miembro del backoffice puedo ejecutar stop (`POST /api/admin/fichaje/admin/stop`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0106 | Backoffice | Sin sesion valida, `POST /api/admin/fichaje/admin/stop` | Respuesta 401/403, nunca datos | pendiente |
| US-0107 | Backoffice | `POST /api/admin/fichaje/admin/stop` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0108 | Backoffice | Como miembro del backoffice puedo consultar entries (`GET /api/admin/fichaje/entries`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0109 | Backoffice | Sin sesion valida, `GET /api/admin/fichaje/entries` | Respuesta 401/403, nunca datos | pendiente |
| US-0110 | Backoffice | Como miembro del backoffice puedo actualizar elemento (`PATCH /api/admin/fichaje/entries/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0111 | Backoffice | Sin sesion valida, `PATCH /api/admin/fichaje/entries/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0112 | Backoffice | `PATCH /api/admin/fichaje/entries/{id}` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0113 | Backoffice | Como miembro del backoffice puedo consultar labour cost (`GET /api/admin/fichaje/labour-cost`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0114 | Backoffice | Sin sesion valida, `GET /api/admin/fichaje/labour-cost` | Respuesta 401/403, nunca datos | pendiente |
| US-0115 | Backoffice | Como miembro del backoffice puedo consultar ws (`GET /api/admin/fichaje/ws`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0116 | Backoffice | Sin sesion valida, `GET /api/admin/fichaje/ws` | Respuesta 401/403, nunca datos | pendiente |
| US-0117 | Backoffice | Como miembro del backoffice puedo consultar horarios (`GET /api/admin/horarios`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0118 | Backoffice | Sin sesion valida, `GET /api/admin/horarios` | Respuesta 401/403, nunca datos | pendiente |
| US-0119 | Backoffice | Como miembro del backoffice puedo ejecutar horarios (`POST /api/admin/horarios`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0120 | Backoffice | Sin sesion valida, `POST /api/admin/horarios` | Respuesta 401/403, nunca datos | pendiente |
| US-0121 | Backoffice | `POST /api/admin/horarios` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0122 | Backoffice | Como miembro del backoffice puedo consultar month (`GET /api/admin/horarios/month`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0123 | Backoffice | Sin sesion valida, `GET /api/admin/horarios/month` | Respuesta 401/403, nunca datos | pendiente |
| US-0124 | Backoffice | Como miembro del backoffice puedo consultar calendar (`GET /api/admin/calendar`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0125 | Backoffice | Sin sesion valida, `GET /api/admin/calendar` | Respuesta 401/403, nunca datos | pendiente |
| US-0126 | Backoffice | Como miembro del backoffice puedo consultar bookings (`GET /api/admin/bookings`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0127 | Backoffice | Sin sesion valida, `GET /api/admin/bookings` | Respuesta 401/403, nunca datos | pendiente |
| US-0128 | Backoffice | Como miembro del backoffice puedo consultar export (`GET /api/admin/bookings/export`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0129 | Backoffice | Sin sesion valida, `GET /api/admin/bookings/export` | Respuesta 401/403, nunca datos | pendiente |
| US-0130 | Backoffice | Como miembro del backoffice puedo consultar search (`GET /api/admin/bookings/search`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0131 | Backoffice | Sin sesion valida, `GET /api/admin/bookings/search` | Respuesta 401/403, nunca datos | pendiente |
| US-0132 | Backoffice | Como miembro del backoffice puedo consultar elemento (`GET /api/admin/bookings/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0133 | Backoffice | Sin sesion valida, `GET /api/admin/bookings/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0134 | Backoffice | Como miembro del backoffice puedo ejecutar bookings (`POST /api/admin/bookings`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0135 | Backoffice | Sin sesion valida, `POST /api/admin/bookings` | Respuesta 401/403, nunca datos | pendiente |
| US-0136 | Backoffice | `POST /api/admin/bookings` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0137 | Backoffice | Como miembro del backoffice puedo actualizar elemento (`PATCH /api/admin/bookings/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0138 | Backoffice | Sin sesion valida, `PATCH /api/admin/bookings/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0139 | Backoffice | `PATCH /api/admin/bookings/{id}` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0140 | Backoffice | Como miembro del backoffice puedo ejecutar cancel (`POST /api/admin/bookings/{id}/cancel`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0141 | Backoffice | Sin sesion valida, `POST /api/admin/bookings/{id}/cancel` | Respuesta 401/403, nunca datos | pendiente |
| US-0142 | Backoffice | `POST /api/admin/bookings/{id}/cancel` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0143 | Backoffice | Como miembro del backoffice puedo consultar documents (`GET /api/admin/bookings/{id}/documents`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0144 | Backoffice | Sin sesion valida, `GET /api/admin/bookings/{id}/documents` | Respuesta 401/403, nunca datos | pendiente |
| US-0145 | Backoffice | Como miembro del backoffice puedo consultar file (`GET /api/admin/bookings/documents/{docId}/file`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0146 | Backoffice | Sin sesion valida, `GET /api/admin/bookings/documents/{docId}/file` | Respuesta 401/403, nunca datos | pendiente |
| US-0147 | Backoffice | Como miembro del backoffice puedo consultar ws (`GET /api/admin/bookings/documents/ws`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0148 | Backoffice | Sin sesion valida, `GET /api/admin/bookings/documents/ws` | Respuesta 401/403, nunca datos | pendiente |
| US-0149 | Backoffice | Como miembro del backoffice puedo consultar arroz types (`GET /api/admin/arroz-types`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0150 | Backoffice | Sin sesion valida, `GET /api/admin/arroz-types` | Respuesta 401/403, nunca datos | pendiente |
| US-0151 | Backoffice | Como miembro del backoffice puedo consultar group menus v2?includeDrafts=0|1 (`GET /api/admin/group-menus-v2?includeDrafts=0|1`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0152 | Backoffice | Sin sesion valida, `GET /api/admin/group-menus-v2?includeDrafts=0|1` | Respuesta 401/403, nunca datos | pendiente |
| US-0153 | Backoffice | Como miembro del backoffice puedo ejecutar drafts (`POST /api/admin/group-menus-v2/drafts`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0154 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/drafts` | Respuesta 401/403, nunca datos | pendiente |
| US-0155 | Backoffice | `POST /api/admin/group-menus-v2/drafts` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0156 | Backoffice | Como miembro del backoffice puedo consultar elemento (`GET /api/admin/group-menus-v2/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0157 | Backoffice | Sin sesion valida, `GET /api/admin/group-menus-v2/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0158 | Backoffice | Como miembro del backoffice puedo actualizar basics (`PATCH /api/admin/group-menus-v2/{id}/basics`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0159 | Backoffice | Sin sesion valida, `PATCH /api/admin/group-menus-v2/{id}/basics` | Respuesta 401/403, nunca datos | pendiente |
| US-0160 | Backoffice | `PATCH /api/admin/group-menus-v2/{id}/basics` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0161 | Backoffice | Como miembro del backoffice puedo actualizar menu type (`PATCH /api/admin/group-menus-v2/{id}/menu-type`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0162 | Backoffice | Sin sesion valida, `PATCH /api/admin/group-menus-v2/{id}/menu-type` | Respuesta 401/403, nunca datos | pendiente |
| US-0163 | Backoffice | `PATCH /api/admin/group-menus-v2/{id}/menu-type` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0164 | Backoffice | Como miembro del backoffice puedo actualizar sections (`PUT /api/admin/group-menus-v2/{id}/sections`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0165 | Backoffice | Sin sesion valida, `PUT /api/admin/group-menus-v2/{id}/sections` | Respuesta 401/403, nunca datos | pendiente |
| US-0166 | Backoffice | `PUT /api/admin/group-menus-v2/{id}/sections` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0167 | Backoffice | Como miembro del backoffice puedo actualizar annotations (`PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/annotations`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0168 | Backoffice | Sin sesion valida, `PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/annotations` | Respuesta 401/403, nunca datos | pendiente |
| US-0169 | Backoffice | `PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/annotations` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0170 | Backoffice | Como miembro del backoffice puedo consultar dishes (`GET /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0171 | Backoffice | Sin sesion valida, `GET /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes` | Respuesta 401/403, nunca datos | pendiente |
| US-0172 | Backoffice | Como miembro del backoffice puedo actualizar dishes (`PUT /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0173 | Backoffice | Sin sesion valida, `PUT /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes` | Respuesta 401/403, nunca datos | pendiente |
| US-0174 | Backoffice | `PUT /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0175 | Backoffice | Como miembro del backoffice puedo actualizar elemento (`PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0176 | Backoffice | Sin sesion valida, `PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}` | Respuesta 401/403, nunca datos | pendiente |
| US-0177 | Backoffice | `PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0178 | Backoffice | Como miembro del backoffice puedo ejecutar image (`POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0179 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image` | Respuesta 401/403, nunca datos | pendiente |
| US-0180 | Backoffice | `POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0181 | Backoffice | Como miembro del backoffice puedo ejecutar ai (`POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image/ai`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0182 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image/ai` | Respuesta 401/403, nunca datos | pendiente |
| US-0183 | Backoffice | `POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image/ai` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0184 | Backoffice | Como miembro del backoffice puedo ejecutar preview image (`POST /api/admin/group-menus-v2/{id}/preview-image`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0185 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/preview-image` | Respuesta 401/403, nunca datos | pendiente |
| US-0186 | Backoffice | `POST /api/admin/group-menus-v2/{id}/preview-image` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0187 | Backoffice | Como miembro del backoffice puedo ejecutar ai (`POST /api/admin/group-menus-v2/{id}/preview-image/ai`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0188 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/preview-image/ai` | Respuesta 401/403, nunca datos | pendiente |
| US-0189 | Backoffice | `POST /api/admin/group-menus-v2/{id}/preview-image/ai` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0190 | Backoffice | Como miembro del backoffice puedo consultar slider (`GET /api/admin/group-menus-v2/{id}/slider`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0191 | Backoffice | Sin sesion valida, `GET /api/admin/group-menus-v2/{id}/slider` | Respuesta 401/403, nunca datos | pendiente |
| US-0192 | Backoffice | Como miembro del backoffice puedo actualizar slider (`PATCH /api/admin/group-menus-v2/{id}/slider`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0193 | Backoffice | Sin sesion valida, `PATCH /api/admin/group-menus-v2/{id}/slider` | Respuesta 401/403, nunca datos | pendiente |
| US-0194 | Backoffice | `PATCH /api/admin/group-menus-v2/{id}/slider` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0195 | Backoffice | Como miembro del backoffice puedo ejecutar images (`POST /api/admin/group-menus-v2/{id}/slider/images`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0196 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/slider/images` | Respuesta 401/403, nunca datos | pendiente |
| US-0197 | Backoffice | `POST /api/admin/group-menus-v2/{id}/slider/images` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0198 | Backoffice | Como miembro del backoffice puedo eliminar elemento (`DELETE /api/admin/group-menus-v2/{id}/slider/images/{imageId}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0199 | Backoffice | Sin sesion valida, `DELETE /api/admin/group-menus-v2/{id}/slider/images/{imageId}` | Respuesta 401/403, nunca datos | pendiente |
| US-0200 | Backoffice | Como miembro del backoffice puedo actualizar images (`PUT /api/admin/group-menus-v2/{id}/slider/images`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0201 | Backoffice | Sin sesion valida, `PUT /api/admin/group-menus-v2/{id}/slider/images` | Respuesta 401/403, nunca datos | pendiente |
| US-0202 | Backoffice | `PUT /api/admin/group-menus-v2/{id}/slider/images` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0203 | Backoffice | Como miembro del backoffice puedo ejecutar ai (`POST /api/admin/group-menus-v2/{id}/slider/images/ai`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0204 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/slider/images/ai` | Respuesta 401/403, nunca datos | pendiente |
| US-0205 | Backoffice | `POST /api/admin/group-menus-v2/{id}/slider/images/ai` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0206 | Backoffice | Como miembro del backoffice puedo consultar ws?menuId=elemento (`GET /api/admin/group-menus-v2/ws?menuId={id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0207 | Backoffice | Sin sesion valida, `GET /api/admin/group-menus-v2/ws?menuId={id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0208 | Backoffice | Como miembro del backoffice puedo ejecutar special image (`POST /api/admin/group-menus-v2/{id}/special-image`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0209 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/special-image` | Respuesta 401/403, nunca datos | pendiente |
| US-0210 | Backoffice | `POST /api/admin/group-menus-v2/{id}/special-image` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0211 | Backoffice | Como miembro del backoffice puedo ejecutar publish (`POST /api/admin/group-menus-v2/{id}/publish`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0212 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/publish` | Respuesta 401/403, nunca datos | pendiente |
| US-0213 | Backoffice | `POST /api/admin/group-menus-v2/{id}/publish` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0214 | Backoffice | Como miembro del backoffice puedo ejecutar toggle active (`POST /api/admin/group-menus-v2/{id}/toggle-active`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0215 | Backoffice | Sin sesion valida, `POST /api/admin/group-menus-v2/{id}/toggle-active` | Respuesta 401/403, nunca datos | pendiente |
| US-0216 | Backoffice | `POST /api/admin/group-menus-v2/{id}/toggle-active` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0217 | Backoffice | Como miembro del backoffice puedo eliminar elemento (`DELETE /api/admin/group-menus-v2/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0218 | Backoffice | Sin sesion valida, `DELETE /api/admin/group-menus-v2/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0219 | Backoffice | Como miembro del backoffice puedo consultar search?q=<text>&limit=<n> (`GET /api/admin/dishes-catalog/search?q=<text>&limit=<n>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0220 | Backoffice | Sin sesion valida, `GET /api/admin/dishes-catalog/search?q=<text>&limit=<n>` | Respuesta 401/403, nunca datos | pendiente |
| US-0221 | Backoffice | Como miembro del backoffice puedo ejecutar upsert (`POST /api/admin/dishes-catalog/upsert`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0222 | Backoffice | Sin sesion valida, `POST /api/admin/dishes-catalog/upsert` | Respuesta 401/403, nunca datos | pendiente |
| US-0223 | Backoffice | `POST /api/admin/dishes-catalog/upsert` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0224 | Backoffice | Como miembro del backoffice puedo consultar restaurant info (`GET /api/admin/config/restaurant-info`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0225 | Backoffice | Sin sesion valida, `GET /api/admin/config/restaurant-info` | Respuesta 401/403, nunca datos | pendiente |
| US-0226 | Backoffice | Como miembro del backoffice puedo ejecutar check website (`POST /api/admin/config/check-website`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0227 | Backoffice | Sin sesion valida, `POST /api/admin/config/check-website` | Respuesta 401/403, nunca datos | pendiente |
| US-0228 | Backoffice | `POST /api/admin/config/check-website` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0229 | Backoffice | Como miembro del backoffice puedo ejecutar restaurant info (`POST /api/admin/config/restaurant-info`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0230 | Backoffice | Sin sesion valida, `POST /api/admin/config/restaurant-info` | Respuesta 401/403, nunca datos | pendiente |
| US-0231 | Backoffice | `POST /api/admin/config/restaurant-info` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0232 | Backoffice | Como miembro del backoffice puedo consultar defaults (`GET /api/admin/config/defaults`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0233 | Backoffice | Sin sesion valida, `GET /api/admin/config/defaults` | Respuesta 401/403, nunca datos | pendiente |
| US-0234 | Backoffice | Como miembro del backoffice puedo ejecutar defaults (`POST /api/admin/config/defaults`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0235 | Backoffice | Sin sesion valida, `POST /api/admin/config/defaults` | Respuesta 401/403, nunca datos | pendiente |
| US-0236 | Backoffice | `POST /api/admin/config/defaults` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0237 | Backoffice | Como miembro del backoffice puedo consultar location booking?date=YYYY MM DD (`GET /api/admin/config/location-booking?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0238 | Backoffice | Sin sesion valida, `GET /api/admin/config/location-booking?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0239 | Backoffice | Como miembro del backoffice puedo ejecutar location booking (`POST /api/admin/config/location-booking`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0240 | Backoffice | Sin sesion valida, `POST /api/admin/config/location-booking` | Respuesta 401/403, nunca datos | pendiente |
| US-0241 | Backoffice | `POST /api/admin/config/location-booking` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0242 | Backoffice | Como miembro del backoffice puedo ejecutar date (`POST /api/admin/config/floors/date`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0243 | Backoffice | Sin sesion valida, `POST /api/admin/config/floors/date` | Respuesta 401/403, nunca datos | pendiente |
| US-0244 | Backoffice | `POST /api/admin/config/floors/date` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0245 | Backoffice | Como miembro del backoffice puedo consultar day?date=YYYY MM DD (`GET /api/admin/config/day?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0246 | Backoffice | Sin sesion valida, `GET /api/admin/config/day?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0247 | Backoffice | Como miembro del backoffice puedo ejecutar day (`POST /api/admin/config/day`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0248 | Backoffice | Sin sesion valida, `POST /api/admin/config/day` | Respuesta 401/403, nunca datos | pendiente |
| US-0249 | Backoffice | `POST /api/admin/config/day` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0250 | Backoffice | Como miembro del backoffice puedo consultar opening hours?date=YYYY MM DD (`GET /api/admin/config/opening-hours?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0251 | Backoffice | Sin sesion valida, `GET /api/admin/config/opening-hours?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0252 | Backoffice | Como miembro del backoffice puedo ejecutar opening hours (`POST /api/admin/config/opening-hours`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0253 | Backoffice | Sin sesion valida, `POST /api/admin/config/opening-hours` | Respuesta 401/403, nunca datos | pendiente |
| US-0254 | Backoffice | `POST /api/admin/config/opening-hours` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0255 | Backoffice | Como miembro del backoffice puedo consultar daily limit?date=YYYY MM DD (`GET /api/admin/config/daily-limit?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0256 | Backoffice | Sin sesion valida, `GET /api/admin/config/daily-limit?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0257 | Backoffice | Como miembro del backoffice puedo ejecutar daily limit (`POST /api/admin/config/daily-limit`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0258 | Backoffice | Sin sesion valida, `POST /api/admin/config/daily-limit` | Respuesta 401/403, nunca datos | pendiente |
| US-0259 | Backoffice | `POST /api/admin/config/daily-limit` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0260 | Backoffice | Como miembro del backoffice puedo consultar mesas de dos?date=YYYY MM DD (`GET /api/admin/config/mesas-de-dos?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0261 | Backoffice | Sin sesion valida, `GET /api/admin/config/mesas-de-dos?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0262 | Backoffice | Como miembro del backoffice puedo ejecutar mesas de dos (`POST /api/admin/config/mesas-de-dos`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0263 | Backoffice | Sin sesion valida, `POST /api/admin/config/mesas-de-dos` | Respuesta 401/403, nunca datos | pendiente |
| US-0264 | Backoffice | `POST /api/admin/config/mesas-de-dos` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0265 | Backoffice | Como miembro del backoffice puedo consultar mesas de tres?date=YYYY MM DD (`GET /api/admin/config/mesas-de-tres?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0266 | Backoffice | Sin sesion valida, `GET /api/admin/config/mesas-de-tres?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0267 | Backoffice | Como miembro del backoffice puedo ejecutar mesas de tres (`POST /api/admin/config/mesas-de-tres`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0268 | Backoffice | Sin sesion valida, `POST /api/admin/config/mesas-de-tres` | Respuesta 401/403, nunca datos | pendiente |
| US-0269 | Backoffice | `POST /api/admin/config/mesas-de-tres` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0270 | Backoffice | Como miembro del backoffice puedo consultar defaults (`GET /api/admin/config/floors/defaults`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0271 | Backoffice | Sin sesion valida, `GET /api/admin/config/floors/defaults` | Respuesta 401/403, nunca datos | pendiente |
| US-0272 | Backoffice | Como miembro del backoffice puedo ejecutar defaults (`POST /api/admin/config/floors/defaults`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0273 | Backoffice | Sin sesion valida, `POST /api/admin/config/floors/defaults` | Respuesta 401/403, nunca datos | pendiente |
| US-0274 | Backoffice | `POST /api/admin/config/floors/defaults` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0275 | Backoffice | Como miembro del backoffice puedo consultar floors?date=YYYY MM DD (`GET /api/admin/config/floors?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0276 | Backoffice | Sin sesion valida, `GET /api/admin/config/floors?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0277 | Backoffice | Como miembro del backoffice puedo ejecutar floors (`POST /api/admin/config/floors`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0278 | Backoffice | Sin sesion valida, `POST /api/admin/config/floors` | Respuesta 401/403, nunca datos | pendiente |
| US-0279 | Backoffice | `POST /api/admin/config/floors` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0280 | API | Como cliente de la API puedo consultar menu visibility (`GET /api/menu-visibility`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0281 | API | Sin sesion valida, `GET /api/menu-visibility` | Respuesta 401/403, nunca datos | pendiente |
| US-0282 | API | Como cliente de la API puedo consultar home (`GET /api/menus/home`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0283 | API | Sin sesion valida, `GET /api/menus/home` | Respuesta 401/403, nunca datos | pendiente |
| US-0284 | API | Como cliente de la API puedo consultar public (`GET /api/menus/public`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0285 | API | Sin sesion valida, `GET /api/menus/public` | Respuesta 401/403, nunca datos | pendiente |
| US-0286 | API | Como cliente de la API puedo consultar dia (`GET /api/menus/dia`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0287 | API | Sin sesion valida, `GET /api/menus/dia` | Respuesta 401/403, nunca datos | pendiente |
| US-0288 | API | Como cliente de la API puedo consultar finde (`GET /api/menus/finde`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0289 | API | Sin sesion valida, `GET /api/menus/finde` | Respuesta 401/403, nunca datos | pendiente |
| US-0290 | API | Como cliente de la API puedo consultar postres (`GET /api/postres`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0291 | API | Sin sesion valida, `GET /api/postres` | Respuesta 401/403, nunca datos | pendiente |
| US-0292 | API | Como cliente de la API puedo consultar vinos (`GET /api/vinos`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0293 | API | Sin sesion valida, `GET /api/vinos` | Respuesta 401/403, nunca datos | pendiente |
| US-0294 | API | Como cliente de la API puedo consultar api_vinos.php (`GET /api/api_vinos.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0295 | API | Sin sesion valida, `GET /api/api_vinos.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0296 | API | Como cliente de la API puedo ejecutar vinos (`POST /api/vinos`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0297 | API | Sin sesion valida, `POST /api/vinos` | Respuesta 401/403, nunca datos | pendiente |
| US-0298 | API | `POST /api/vinos` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0299 | API | Como cliente de la API puedo ejecutar api_vinos.php (`POST /api/api_vinos.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0300 | API | Sin sesion valida, `POST /api/api_vinos.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0301 | API | `POST /api/api_vinos.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0302 | API | Como cliente de la API puedo ejecutar toggleMenuVisibility.php (`POST /api/menuVisibilityBackend/toggleMenuVisibility.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0303 | API | Sin sesion valida, `POST /api/menuVisibilityBackend/toggleMenuVisibility.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0304 | API | `POST /api/menuVisibilityBackend/toggleMenuVisibility.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0305 | API | Como cliente de la API puedo ejecutar updateDishDia.php (`POST /api/updateDishDia.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0306 | API | Sin sesion valida, `POST /api/updateDishDia.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0307 | API | `POST /api/updateDishDia.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0308 | API | Como cliente de la API puedo ejecutar toggleDishStatusDia.php (`POST /api/toggleDishStatusDia.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0309 | API | Sin sesion valida, `POST /api/toggleDishStatusDia.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0310 | API | `POST /api/toggleDishStatusDia.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0311 | API | Como cliente de la API puedo consultar searchDishesDia.php (`GET /api/searchDishesDia.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0312 | API | Sin sesion valida, `GET /api/searchDishesDia.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0313 | API | Como cliente de la API puedo ejecutar updateDish.php (`POST /api/updateDish.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0314 | API | Sin sesion valida, `POST /api/updateDish.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0315 | API | `POST /api/updateDish.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0316 | API | Como cliente de la API puedo ejecutar toggleDishStatus.php (`POST /api/toggleDishStatus.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0317 | API | Sin sesion valida, `POST /api/toggleDishStatus.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0318 | API | `POST /api/toggleDishStatus.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0319 | API | Como cliente de la API puedo consultar searchDishesFinde.php (`GET /api/searchDishesFinde.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0320 | API | Sin sesion valida, `GET /api/searchDishesFinde.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0321 | API | Como cliente de la API puedo consultar updatePostre.php (`GET /api/updatePostre.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0322 | API | Sin sesion valida, `GET /api/updatePostre.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0323 | API | Como cliente de la API puedo ejecutar updatePostre.php (`POST /api/updatePostre.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0324 | API | Sin sesion valida, `POST /api/updatePostre.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0325 | API | `POST /api/updatePostre.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0326 | API | Como cliente de la API puedo consultar searchPostres.php (`GET /api/searchPostres.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0327 | API | Sin sesion valida, `GET /api/searchPostres.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0328 | API | Como cliente de la API puedo consultar getAllMenus.php (`GET /api/menuDeGruposBackend/getAllMenus.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0329 | API | Sin sesion valida, `GET /api/menuDeGruposBackend/getAllMenus.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0330 | API | Como cliente de la API puedo consultar getMenu.php?id=<id> (`GET /api/menuDeGruposBackend/getMenu.php?id=<id>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0331 | API | Sin sesion valida, `GET /api/menuDeGruposBackend/getMenu.php?id=<id>` | Respuesta 401/403, nunca datos | pendiente |
| US-0332 | API | Como cliente de la API puedo consultar getActiveMenusForDisplay.php (`GET /api/menuDeGruposBackend/getActiveMenusForDisplay.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0333 | API | Sin sesion valida, `GET /api/menuDeGruposBackend/getActiveMenusForDisplay.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0334 | API | Como cliente de la API puedo consultar getMenuForDisplay?id=<id> (`GET /api/menuDeGruposBackend/getMenuForDisplay?id=<id>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0335 | API | Sin sesion valida, `GET /api/menuDeGruposBackend/getMenuForDisplay?id=<id>` | Respuesta 401/403, nunca datos | pendiente |
| US-0336 | API | Como cliente de la API puedo ejecutar addMenu.php (`POST /api/menuDeGruposBackend/addMenu.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0337 | API | Sin sesion valida, `POST /api/menuDeGruposBackend/addMenu.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0338 | API | `POST /api/menuDeGruposBackend/addMenu.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0339 | API | Como cliente de la API puedo ejecutar updateMenu.php (`POST /api/menuDeGruposBackend/updateMenu.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0340 | API | Sin sesion valida, `POST /api/menuDeGruposBackend/updateMenu.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0341 | API | `POST /api/menuDeGruposBackend/updateMenu.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0342 | API | Como cliente de la API puedo actualizar updateMenu.php (`PUT /api/menuDeGruposBackend/updateMenu.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0343 | API | Sin sesion valida, `PUT /api/menuDeGruposBackend/updateMenu.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0344 | API | `PUT /api/menuDeGruposBackend/updateMenu.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0345 | API | Como cliente de la API puedo ejecutar toggleActive.php (`POST /api/menuDeGruposBackend/toggleActive.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0346 | API | Sin sesion valida, `POST /api/menuDeGruposBackend/toggleActive.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0347 | API | `POST /api/menuDeGruposBackend/toggleActive.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0348 | API | Como cliente de la API puedo ejecutar deleteMenu.php (`POST /api/menuDeGruposBackend/deleteMenu.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0349 | API | Sin sesion valida, `POST /api/menuDeGruposBackend/deleteMenu.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0350 | API | `POST /api/menuDeGruposBackend/deleteMenu.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0351 | API | Como cliente de la API puedo eliminar deleteMenu.php (`DELETE /api/menuDeGruposBackend/deleteMenu.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0352 | API | Sin sesion valida, `DELETE /api/menuDeGruposBackend/deleteMenu.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0353 | API | Como cliente de la API puedo consultar rice types (`GET /api/reservations/rice-types`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0354 | API | Sin sesion valida, `GET /api/reservations/rice-types` | Respuesta 401/403, nunca datos | pendiente |
| US-0355 | API | Como cliente de la API puedo consultar group menus?party_size=<number> (`GET /api/reservations/group-menus?party_size=<number>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0356 | API | Sin sesion valida, `GET /api/reservations/group-menus?party_size=<number>` | Respuesta 401/403, nunca datos | pendiente |
| US-0357 | API | Como cliente de la API puedo consultar mandatory menus?date=YYYY MM DD (`GET /api/reservations/mandatory-menus?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0358 | API | Sin sesion valida, `GET /api/reservations/mandatory-menus?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0359 | API | Como cliente de la API puedo ejecutar fetch_daily_limit.php (`POST /api/fetch_daily_limit.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0360 | API | Sin sesion valida, `POST /api/fetch_daily_limit.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0361 | API | `POST /api/fetch_daily_limit.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0362 | API | Como cliente de la API puedo consultar month availability (`GET /api/reservations/month-availability`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0363 | API | Sin sesion valida, `GET /api/reservations/month-availability` | Respuesta 401/403, nunca datos | pendiente |
| US-0364 | API | Como cliente de la API puedo consultar closed days (`GET /api/reservations/closed-days`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0365 | API | Sin sesion valida, `GET /api/reservations/closed-days` | Respuesta 401/403, nunca datos | pendiente |
| US-0366 | API | Como cliente de la API puedo consultar getopeninghours.php (`GET /api/getopeninghours.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0367 | API | Sin sesion valida, `GET /api/getopeninghours.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0368 | API | Como cliente de la API puedo ejecutar editopeninghours.php (`POST /api/editopeninghours.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0369 | API | Sin sesion valida, `POST /api/editopeninghours.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0370 | API | `POST /api/editopeninghours.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0371 | API | Como cliente de la API puedo consultar gethourpercentages.php (`GET /api/gethourpercentages.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0372 | API | Sin sesion valida, `GET /api/gethourpercentages.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0373 | API | Como cliente de la API puedo ejecutar updatehourpercentages.php (`POST /api/updatehourpercentages.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0374 | API | Sin sesion valida, `POST /api/updatehourpercentages.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0375 | API | `POST /api/updatehourpercentages.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0376 | API | Como cliente de la API puedo consultar get_calendar_data.php (`GET /api/get_calendar_data.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0377 | API | Sin sesion valida, `GET /api/get_calendar_data.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0378 | API | Como cliente de la API puedo consultar getValidMenusForPartySize.php (`GET /api/getValidMenusForPartySize.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0379 | API | Sin sesion valida, `GET /api/getValidMenusForPartySize.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0380 | API | Como cliente de la API puedo consultar get_booking_availability_context.php (`GET /api/get_booking_availability_context.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0381 | API | Sin sesion valida, `GET /api/get_booking_availability_context.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0382 | API | Como cliente de la API puedo ejecutar get_booking_availability_context.php (`POST /api/get_booking_availability_context.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0383 | API | Sin sesion valida, `POST /api/get_booking_availability_context.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0384 | API | `POST /api/get_booking_availability_context.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0385 | API | Como cliente de la API puedo consultar get_available_rice_types.php (`GET /api/get_available_rice_types.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0386 | API | Sin sesion valida, `GET /api/get_available_rice_types.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0387 | API | Como cliente de la API puedo ejecutar check_date_availability.php (`POST /api/check_date_availability.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0388 | API | Sin sesion valida, `POST /api/check_date_availability.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0389 | API | `POST /api/check_date_availability.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0390 | API | Como cliente de la API puedo ejecutar check_party_size_availability.php (`POST /api/check_party_size_availability.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0391 | API | Sin sesion valida, `POST /api/check_party_size_availability.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0392 | API | `POST /api/check_party_size_availability.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0393 | API | Como cliente de la API puedo ejecutar validate_booking_modifiable.php (`POST /api/validate_booking_modifiable.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0394 | API | Sin sesion valida, `POST /api/validate_booking_modifiable.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0395 | API | `POST /api/validate_booking_modifiable.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0396 | API | Como cliente de la API puedo ejecutar update_reservation.php (`POST /api/update_reservation.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0397 | API | Sin sesion valida, `POST /api/update_reservation.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0398 | API | `POST /api/update_reservation.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0399 | API | Como cliente de la API puedo ejecutar save_modification_history.php (`POST /api/save_modification_history.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0400 | API | Sin sesion valida, `POST /api/save_modification_history.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0401 | API | `POST /api/save_modification_history.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0402 | API | Como cliente de la API puedo ejecutar notify_restaurant_modification.php (`POST /api/notify_restaurant_modification.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0403 | API | Sin sesion valida, `POST /api/notify_restaurant_modification.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0404 | API | `POST /api/notify_restaurant_modification.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0405 | API | Como cliente de la API puedo consultar n8nReminder.php (`GET /api/n8nReminder.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0406 | API | Sin sesion valida, `GET /api/n8nReminder.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0407 | API | Como cliente de la API puedo consultar confirm_reservation.php (`GET /confirm_reservation.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0408 | API | Sin sesion valida, `GET /confirm_reservation.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0409 | API | Como cliente de la API puedo ejecutar confirm_reservation.php (`POST /confirm_reservation.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0410 | API | Sin sesion valida, `POST /confirm_reservation.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0411 | API | `POST /confirm_reservation.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0412 | API | Como cliente de la API puedo consultar cancel_reservation.php (`GET /cancel_reservation.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0413 | API | Sin sesion valida, `GET /cancel_reservation.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0414 | API | Como cliente de la API puedo ejecutar cancel_reservation.php (`POST /cancel_reservation.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0415 | API | Sin sesion valida, `POST /cancel_reservation.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0416 | API | `POST /cancel_reservation.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0417 | API | Como cliente de la API puedo consultar book_rice.php (`GET /book_rice.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0418 | API | Sin sesion valida, `GET /book_rice.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0419 | API | Como cliente de la API puedo ejecutar book_rice.php (`POST /book_rice.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0420 | API | Sin sesion valida, `POST /book_rice.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0421 | API | `POST /book_rice.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0422 | API | Como cliente de la API puedo ejecutar navidad_booking.php (`POST /api/navidad_booking.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0423 | API | Sin sesion valida, `POST /api/navidad_booking.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0424 | API | `POST /api/navidad_booking.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0425 | API | Como cliente de la API puedo ejecutar sendEmailAndWhastappAd.php (`POST /api/emailAdvertising/sendEmailAndWhastappAd.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0426 | API | Sin sesion valida, `POST /api/emailAdvertising/sendEmailAndWhastappAd.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0427 | API | `POST /api/emailAdvertising/sendEmailAndWhastappAd.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0428 | API | Como cliente de la API puedo consultar two top availability?date=<YYYY MM DD> (`GET /api/reservations/two-top-availability?date=<YYYY-MM-DD>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0429 | API | Sin sesion valida, `GET /api/reservations/two-top-availability?date=<YYYY-MM-DD>` | Respuesta 401/403, nunca datos | pendiente |
| US-0430 | API | Como cliente de la API puedo consultar hour data?date=<YYYY MM DD> (`GET /api/reservations/hour-data?date=<YYYY-MM-DD>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0431 | API | Sin sesion valida, `GET /api/reservations/hour-data?date=<YYYY-MM-DD>` | Respuesta 401/403, nunca datos | pendiente |
| US-0432 | API | Como cliente de la API puedo consultar day context?date=<YYYY MM DD> (`GET /api/reservations/day-context?date=<YYYY-MM-DD>`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0433 | API | Sin sesion valida, `GET /api/reservations/day-context?date=<YYYY-MM-DD>` | Respuesta 401/403, nunca datos | pendiente |
| US-0434 | API | Como cliente de la API puedo ejecutar fetch_mesas_de_dos.php (`POST /api/fetch_mesas_de_dos.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0435 | API | Sin sesion valida, `POST /api/fetch_mesas_de_dos.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0436 | API | `POST /api/fetch_mesas_de_dos.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0437 | API | Como cliente de la API puedo ejecutar update_daily_limit.php (`POST /api/update_daily_limit.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0438 | API | Sin sesion valida, `POST /api/update_daily_limit.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0439 | API | `POST /api/update_daily_limit.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0440 | API | Como cliente de la API puedo ejecutar limitemesasdedos.php (`POST /api/limitemesasdedos.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0441 | API | Sin sesion valida, `POST /api/limitemesasdedos.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0442 | API | `POST /api/limitemesasdedos.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0443 | API | Como cliente de la API puedo ejecutar get_mesasdedos_limit.php (`POST /api/get_mesasdedos_limit.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0444 | API | Sin sesion valida, `POST /api/get_mesasdedos_limit.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0445 | API | `POST /api/get_mesasdedos_limit.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0446 | API | Como cliente de la API puedo ejecutar check_day_status.php (`POST /api/check_day_status.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0447 | API | Sin sesion valida, `POST /api/check_day_status.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0448 | API | `POST /api/check_day_status.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0449 | API | Como cliente de la API puedo ejecutar open_day.php (`POST /api/open_day.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0450 | API | Sin sesion valida, `POST /api/open_day.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0451 | API | `POST /api/open_day.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0452 | API | Como cliente de la API puedo ejecutar close_day.php (`POST /api/close_day.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0453 | API | Sin sesion valida, `POST /api/close_day.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0454 | API | `POST /api/close_day.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0455 | API | Como cliente de la API puedo ejecutar fetch_occupancy.php (`POST /api/fetch_occupancy.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0456 | API | Sin sesion valida, `POST /api/fetch_occupancy.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0457 | API | `POST /api/fetch_occupancy.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0458 | API | Como cliente de la API puedo consultar gethourdata.php?date=YYYY MM DD (`GET /api/gethourdata.php?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0459 | API | Sin sesion valida, `GET /api/gethourdata.php?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0460 | API | Como cliente de la API puedo ejecutar savehourdata.php (`POST /api/savehourdata.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0461 | API | Sin sesion valida, `POST /api/savehourdata.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0462 | API | `POST /api/savehourdata.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0463 | API | Como cliente de la API puedo ejecutar front (`POST /api/bookings/front`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0464 | API | Sin sesion valida, `POST /api/bookings/front` | Respuesta 401/403, nunca datos | pendiente |
| US-0465 | API | `POST /api/bookings/front` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0466 | API | Como cliente de la API puedo consultar get_reservation_day_context.php?date=YYYY MM DD (`GET /api/get_reservation_day_context.php?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0467 | API | Sin sesion valida, `GET /api/get_reservation_day_context.php?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0468 | API | Como cliente de la API puedo ejecutar insert_booking.php (`POST /api/insert_booking.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0469 | API | Sin sesion valida, `POST /api/insert_booking.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0470 | API | `POST /api/insert_booking.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0471 | Backoffice | Como miembro del backoffice puedo ejecutar logo (`POST /api/admin/branding/logo`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0472 | Backoffice | Sin sesion valida, `POST /api/admin/branding/logo` | Respuesta 401/403, nunca datos | pendiente |
| US-0473 | Backoffice | `POST /api/admin/branding/logo` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0474 | API | Como cliente de la API puedo ejecutar fetch_bookings.php (`POST /api/fetch_bookings.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0475 | API | Sin sesion valida, `POST /api/fetch_bookings.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0476 | API | `POST /api/fetch_bookings.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0477 | API | Como cliente de la API puedo ejecutar get_booking.php (`POST /api/get_booking.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0478 | API | Sin sesion valida, `POST /api/get_booking.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0479 | API | `POST /api/get_booking.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0480 | API | Como cliente de la API puedo ejecutar edit_booking.php (`POST /api/edit_booking.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0481 | API | Sin sesion valida, `POST /api/edit_booking.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0482 | API | `POST /api/edit_booking.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0483 | API | Como cliente de la API puedo ejecutar delete_booking.php (`POST /api/delete_booking.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0484 | API | Sin sesion valida, `POST /api/delete_booking.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0485 | API | `POST /api/delete_booking.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0486 | API | Como cliente de la API puedo ejecutar update_table_number.php (`POST /api/update_table_number.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0487 | API | Sin sesion valida, `POST /api/update_table_number.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0488 | API | `POST /api/update_table_number.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0489 | API | Como cliente de la API puedo ejecutar get_reservations.php (`POST /api/get_reservations.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0490 | API | Sin sesion valida, `POST /api/get_reservations.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0491 | API | `POST /api/get_reservations.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0492 | API | Como cliente de la API puedo ejecutar fetch_cancelled_bookings.php (`POST /api/fetch_cancelled_bookings.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0493 | API | Sin sesion valida, `POST /api/fetch_cancelled_bookings.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0494 | API | `POST /api/fetch_cancelled_bookings.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0495 | API | Como cliente de la API puedo ejecutar reactivate_booking.php (`POST /api/reactivate_booking.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0496 | API | Sin sesion valida, `POST /api/reactivate_booking.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0497 | API | `POST /api/reactivate_booking.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0498 | API | Como cliente de la API puedo consultar salon_condesa_api.php?date=YYYY MM DD (`GET /api/salon_condesa_api.php?date=YYYY-MM-DD`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0499 | API | Sin sesion valida, `GET /api/salon_condesa_api.php?date=YYYY-MM-DD` | Respuesta 401/403, nunca datos | pendiente |
| US-0500 | API | Como cliente de la API puedo ejecutar salon_condesa_api.php (`POST /api/salon_condesa_api.php`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0501 | API | Sin sesion valida, `POST /api/salon_condesa_api.php` | Respuesta 401/403, nunca datos | pendiente |
| US-0502 | API | `POST /api/salon_condesa_api.php` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0503 | Backoffice | Como miembro del backoffice puedo consultar tables (`GET /api/admin/tables`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0504 | Backoffice | Sin sesion valida, `GET /api/admin/tables` | Respuesta 401/403, nunca datos | pendiente |
| US-0505 | Backoffice | Como miembro del backoffice puedo ejecutar tables (`POST /api/admin/tables`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0506 | Backoffice | Sin sesion valida, `POST /api/admin/tables` | Respuesta 401/403, nunca datos | pendiente |
| US-0507 | Backoffice | `POST /api/admin/tables` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0508 | Backoffice | Como miembro del backoffice puedo actualizar tables (`PUT /api/admin/tables`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0509 | Backoffice | Sin sesion valida, `PUT /api/admin/tables` | Respuesta 401/403, nunca datos | pendiente |
| US-0510 | Backoffice | `PUT /api/admin/tables` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0511 | Backoffice | Como miembro del backoffice puedo ejecutar texture image (`POST /api/admin/tables/{id}/texture-image`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0512 | Backoffice | Sin sesion valida, `POST /api/admin/tables/{id}/texture-image` | Respuesta 401/403, nunca datos | pendiente |
| US-0513 | Backoffice | `POST /api/admin/tables/{id}/texture-image` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0514 | Backoffice | Como miembro del backoffice puedo eliminar elemento (`DELETE /api/admin/tables/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0515 | Backoffice | Sin sesion valida, `DELETE /api/admin/tables/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0516 | Backoffice | Como miembro del backoffice puedo consultar ws (`GET /api/admin/tables/ws`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0517 | Backoffice | Sin sesion valida, `GET /api/admin/tables/ws` | Respuesta 401/403, nunca datos | pendiente |
| US-0518 | Backoffice | Como miembro del backoffice puedo actualizar phone (`PUT /api/admin/members/{id}/phone`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0519 | Backoffice | Sin sesion valida, `PUT /api/admin/members/{id}/phone` | Respuesta 401/403, nunca datos | pendiente |
| US-0520 | Backoffice | `PUT /api/admin/members/{id}/phone` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0521 | Backoffice | Como miembro del backoffice puedo ejecutar subscribe (`POST /api/admin/members/whatsapp/subscribe`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0522 | Backoffice | Sin sesion valida, `POST /api/admin/members/whatsapp/subscribe` | Respuesta 401/403, nunca datos | pendiente |
| US-0523 | Backoffice | `POST /api/admin/members/whatsapp/subscribe` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0524 | Backoffice | Como miembro del backoffice puedo ejecutar connect (`POST /api/admin/members/whatsapp/connect`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0525 | Backoffice | Sin sesion valida, `POST /api/admin/members/whatsapp/connect` | Respuesta 401/403, nunca datos | pendiente |
| US-0526 | Backoffice | `POST /api/admin/members/whatsapp/connect` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0527 | Backoffice | Como miembro del backoffice puedo consultar connection (`GET /api/admin/members/whatsapp/connection`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0528 | Backoffice | Sin sesion valida, `GET /api/admin/members/whatsapp/connection` | Respuesta 401/403, nunca datos | pendiente |
| US-0529 | Backoffice | Como miembro del backoffice puedo ejecutar disconnect (`POST /api/admin/members/whatsapp/disconnect`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0530 | Backoffice | Sin sesion valida, `POST /api/admin/members/whatsapp/disconnect` | Respuesta 401/403, nunca datos | pendiente |
| US-0531 | Backoffice | `POST /api/admin/members/whatsapp/disconnect` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0532 | Backoffice | Como miembro del backoffice puedo consultar servers (`GET /api/admin/integrations/uazapi/servers`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0533 | Backoffice | Sin sesion valida, `GET /api/admin/integrations/uazapi/servers` | Respuesta 401/403, nunca datos | pendiente |
| US-0534 | Backoffice | Como miembro del backoffice puedo ejecutar servers (`POST /api/admin/integrations/uazapi/servers`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0535 | Backoffice | Sin sesion valida, `POST /api/admin/integrations/uazapi/servers` | Respuesta 401/403, nunca datos | pendiente |
| US-0536 | Backoffice | `POST /api/admin/integrations/uazapi/servers` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
| US-0537 | Backoffice | Como miembro del backoffice puedo actualizar elemento (`PATCH /api/admin/integrations/uazapi/servers/{id}`) | Respuesta 2xx con el contrato documentado en ENDPOINTS.md | pendiente |
| US-0538 | Backoffice | Sin sesion valida, `PATCH /api/admin/integrations/uazapi/servers/{id}` | Respuesta 401/403, nunca datos | pendiente |
| US-0539 | Backoffice | `PATCH /api/admin/integrations/uazapi/servers/{id}` con payload invalido | Respuesta 400/422 con mensaje claro, sin escrituras parciales | pendiente |
