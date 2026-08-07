# Forky — trabajo pendiente

## Estado actual

> Última auditoría: backend tools, RBAC, confirmaciones, auditoría, límites, CI y renderer accesible están implementados. Los puntos marcados como integración requieren credenciales/servicios del entorno de despliegue.

Forky ya dispone de:

- Contexto de usuario autenticado y restaurante activo.
- Sesiones e historial persistentes.
- Tools tenant-scoped para información del restaurante.
- CRUD confirmado de reservas.
- Tools de catálogo para comida, bebidas, cafés, vinos, menús, POS, stock y miembros.
- Consultas analíticas para reservas, productos, ingresos y stock.
- Protocolo estructurado `tool_use` / `tool_result`.
- Render frontend de bloques `forky-chart`.

Ramas actuales:

- `backend/feat/forky-complete-backend-tools`
- `backoffice/feat/forky-restaurant-tools`

## Pendientes críticos

### 1. Ejecutar tests con toolchain compatible

- Actualizar entorno CI/dev al toolchain declarado en `backend/go.mod` (actualmente Go 1.26; las dependencias usan APIs de Go moderno). El workflow `backend/.github/workflows/quality.yml` lo configura automáticamente.
- Ejecutar `go test ./...` (CI).
- Ejecutar `go vet ./...` (CI).
- Ejecutar tests de integración con MySQL y migraciones reales.
- Resolver cualquier discrepancia entre columnas asumidas por tools genéricas y esquema real.

### 2. Sustituir SQL genérico por servicios de dominio

Las tools de catálogo actuales usan una allowlist, pero deben reutilizar servicios existentes cuando haya lógica de negocio:

- Reservas: disponibilidad, límites diarios, horarios, arroz, menús, notificaciones.
- POS: stock automático, pagos, caja, cierres y reembolsos.
- Stock: movimientos, transferencias, recetas, producción, costes y auditoría.
- Menús: secciones, platos, suplementos, visibilidad y reglas de grupo.
- Fichaje: invariantes de turnos y entradas activas.

No ejecutar mutaciones directas si el handler/servicio existente contiene reglas adicionales.

### 3. Completar cobertura de tools por dominio

Añadir tools tipadas y específicas para:

- Horarios, días especiales, límites y disponibilidad.
- Clientes y fuentes de clientes.
- Menús, secciones, platos y categorías.
- Miembros, roles, permisos y compensaciones.
- Fichajes, turnos y horarios laborales.
- Stock: almacenes, categorías, unidades, niveles, movimientos, transferencias.
- Recetas, producción, mermas y costes.
- POS: visitas, tickets, líneas, pagos, caja, etiquetas, devoluciones.
- Facturas y exportaciones.
- Configuración del restaurante.
- WhatsApp y configuración del bot.
- Site builder y contenido publicado.
- Analítica completa con comparativas y granularidad.

Cada tool necesita esquema JSON, handler, validación, permisos, auditoría y tests.

### 4. RBAC y autorización por operación

- Resolver permisos del usuario desde la sesión activa.
- Asociar cada tool a un permiso/sección.
- Denegar tools no autorizadas antes de tocar DB.
- No confiar en instrucciones del modelo para autorizar operaciones.
- Aplicar permisos de lectura y escritura por separado.
- Restringir operaciones sensibles a roles adecuados.

### 5. Confirmación server-side

`confirmed=true` no basta como control de seguridad. Implementar:

- Preview de operación antes de mutar.
- Token de confirmación generado por backend.
- Token ligado a usuario, restaurante, tool, argumentos y sesión.
- Expiración corta.
- Un solo uso y protección contra replay.
- Confirmación adicional para borrar, reembolsar, cerrar caja o cambios masivos.
- Registro del motivo/confirmación en auditoría.

### 6. Auditoría

Crear registro estructurado para cada tool call:

- usuario;
- restaurante;
- sesión Forky;
- nombre de tool;
- versión de tool;
- argumentos saneados;
- entidad afectada;
- resultado resumido;
- error;
- timestamp;
- duración;
- correlation/request ID.

No guardar secretos, contraseñas, tokens ni datos innecesarios.

### 7. Protocolo MiniMax/Anthropic

Verificar con respuestas reales:

- SSE `content_block_start` y `content_block_delta`.
- Inputs JSON fragmentados en varios eventos.
- Múltiples `tool_use` en un turno.
- Mensajes assistant con bloques completos.
- Mensajes user con `tool_result` exactos.
- `stop_reason=tool_use` y `stop_reason=end_turn`.
- Errores parciales y reconexión WebSocket.
- No duplicar texto al reintentar.
- Persistir tool turns de forma reproducible.

### 8. Gráficos frontend

Sustituir el renderer CSS mínimo por componentes React/Recharts completos:

- bar;
- line;
- area;
- pie/donut;
- stacked bar;
- comparativas.

Añadir:

- schema validation;
- colores por tema;
- tooltips;
- leyenda;
- estados loading/error/empty;
- tabla accesible equivalente;
- responsive container;
- `prefers-reduced-motion`;
- exportación de datos si procede;
- fallback markdown seguro.

### 9. Tests necesarios

#### Backend

- Catálogo completo de definitions.
- Validación de todos los JSON schemas.
- Routing de cada tool.
- CRUD happy path.
- Validaciones de entrada.
- Aislamiento entre restaurantes.
- RBAC por operación.
- Confirmación, expiración y replay.
- Auditoría.
- Transacciones y rollback.
- Errores DB.
- Tool-use MiniMax realista.
- Múltiples tools por turno.
- WebSocket reconnect y deduplicación.

#### Frontend

- Render de cada tipo de gráfico.
- JSON inválido.
- Datos vacíos.
- Accesibilidad.
- Responsive.
- Dark/light theme.
- Reduced motion.
- Tool status y errores.

#### E2E

- Login de usuario.
- Selección de restaurante A.
- Tool read/write en A.
- Verificación de que B no es accesible.
- Cambio a restaurante B.
- Confirmación de mutación.
- Consulta analítica con gráfico.
- Reconexión WebSocket.

### 10. Documentación API

Documentar por tool:

- nombre;
- propósito;
- JSON schema;
- permisos requeridos;
- restaurant scope;
- side effects;
- confirmación;
- errores;
- respuesta;
- ejemplos;
- versión.

Añadir matriz `tool → endpoint/servicio → permiso → tabla/eventos`.

### 11. Rendimiento y límites

- Timeout por tool.
- Límite de filas y tamaño de respuesta.
- Paginación.
- Rate limit para usuarios autenticados.
- Máximo de tools por turno.
- Máximo de iteraciones por conversación.
- Cancelación por disconnect.
- Evitar consultas N+1.
- Métricas de latencia y errores.

### 12. Seguridad y privacidad

- Validar IDs y ownership siempre en backend.
- No permitir nombres de tabla/columna enviados por el modelo.
- Sanitizar argumentos y markdown.
- No exponer información de otros restaurantes.
- No devolver credenciales ni tokens.
- No permitir mutaciones arbitrarias mediante una tool SQL.
- Revisar prompt injection en datos almacenados.
- Separar tools públicas de tools backoffice.

## Criterio de cierre

La tarea queda completa cuando:

1. Todas las tools están registradas y ejecutables.
2. Cada tool reutiliza lógica de dominio o documenta por qué no.
3. RBAC, ownership y auditoría están activos.
4. Confirmaciones son server-side, expirables y one-shot.
5. CRUD relevante del backend tiene cobertura tipada.
6. Gráficos usan componentes React/Recharts accesibles.
7. Tests unitarios, integración y E2E pasan con Go compatible.
8. No existe fuga cross-tenant.
9. Documentación y matriz de tools están actualizadas.
10. CI ejecuta typecheck, tests, vet, lint y E2E.
