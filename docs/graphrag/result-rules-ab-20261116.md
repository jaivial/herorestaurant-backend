# GraphRAG vs reglas por restaurante: análisis y prueba A/B (16-nov-2026)

Pregunta de Jaime: con `VC_GRAPHRAG=1` (producción desde el 16-nov), ¿sigue el bot cumpliendo las normas de cada restaurante?

**Respuesta corta:** antes del arreglo, no del todo. Los fragmentos del grafo se pintaban bajo `## REGLAS APLICABLES A ESTE MENSAJE`, delante de las reglas reales. Eso causó dos incumplimientos medidos que con OFF no aparecen:
- **Alérgenos (C10):** con el grafo el bot afirmó, en 2 de 2 ejecuciones, si una paella lleva o no sulfitos.
- **Arroces (C04):** con el grafo dejó de avisar, en 2 de 2 ejecuciones, de la regla «una sola variedad por mesa de menos de 8».

Con el arreglo, los datos del grafo van en una sección propia, detrás de todas las reglas y con una línea de precedencia, y sin alérgenos en el camino cliente. Esos dos casos vuelven al nivel de OFF o lo mejoran. El resto de los incumplimientos que quedan ocurren igual con OFF: son del modelo o de los datos, no del grafo.

## 1. Fuentes de normas (inventario)

| Fuente | Dónde | Restaurante 1 | Restaurante 343 |
|---|---|---|---|
| `whatsapp_bot_config.config_json.rules` | `loadBotTenantConfig` | vacío. Si tuviera Rules propias, no se usarían ni el RAG ni el grafo (`whatsapp_bot_webhook.go`, `promptData.Knowledge` solo si `Rules == ""`) | sin fila: defaults |
| `custom_instructions` | sección `## INSTRUCCIONES ESPECÍFICAS` | vacío | — |
| `botDefaultRules` (sin RAG) / `botCoreRules` (con RAG) | `whatsapp_bot_prompt.go` | `botCoreRules` | `botCoreRules` |
| `restaurant_bot_ai_config.knowledge_json` + `botMergeKnowledgeV2` | RAG FTS por turno | 14 chunks + 5 de la v2 (`fechas_especiales`, `eventos`, `negociacion_cercana`, `notas_reserva`, `audios`) | `botDefaultKnowledge()` (texto de Villa Carmen) |
| Guards previos al modelo (camino cliente) | `botHardPolicyGuard` (horizonte de 35 días), mismo día, extras y alérgenos (regex `botAllergenIntentRe`) | sí | sí |
| Bloque fijo `## CARTA Y HORARIOS` (política de menú y herramientas) | prompt | sí | sí |
| Datos del grafo (`graph-ingest`) | `botRetrieveKnowledge`, hasta 4 snippets | 193 nodos | 2 nodos (restaurante y «Menú del Mar») |

## 2. Análisis estático: norma → riesgo → evidencia

| Norma | Qué del grafo puede contradecirla o tentar a saltarla | Riesgo | Evidencia |
|---|---|---|---|
| Alérgenos e ingredientes (no confirmar; derivar al restaurante) | Los bodies de `menudish`/`dish`/`dessert`/`drink` llevan `Alérgenos: …`, y la ausencia del campo se lee como «no lleva». Las descripciones de los platos incluyen ingredientes. La regex del guard no cubre `sulfitos`, `marisco`, `pescado`, `huevo`… | **alto** | C10 ON-antes: «la Paella Valenciana clásica *no lleva sulfitos ni marisco*» y «lleva *sulfitos* como alérgeno» |
| Arroces: lista exacta de `get_rice_menu` y una sola variedad en mesas de menos de 8 | Arroces de todos los menús (día, fin de semana, grupo) como «reglas». Los comentarios de los menús de grupo dicen «máx. 1 seco y 1 meloso por mesa» | medio | C04 ON-antes 0/2 avisa de la variedad única (OFF 2/2). C05: conflicto de datos (ver pendientes) |
| Menú cerrado obligatorio | `comida_items` (7 cafés de carta), precios y suplementos sueltos, menús de grupo | bajo | C01/C02/C19/C20 cumplen en las 3 variantes. En C01 ON-antes, 4 cafés aparecían como «reglas» (ruido) |
| Niños / no hay menú infantil | La FAQ del «Menú Navidad» dice «Hay un menú infantil (35 €)», que solo vale para esa fecha | medio | Snippet presente en C03 ON-antes; el modelo no lo usó |
| Disponibilidad, horarios y mismo día (usar siempre herramientas) | Nodos «Horario habitual» y «Turnos del <fecha>» (60 días), con excepciones | bajo | C14/C16 llaman a las herramientas en todas las variantes. El mismo día lo corta el guard antes del modelo |
| Fechas especiales (no se reservan por WhatsApp) | Menús de tipo 6 (Navidad, Nochevieja, Reyes) con FAQ «Desde el botón de reserva» y precio dentro de la FAQ | bajo | C18: el guard de horizonte manda en todas. ON-después da además el menú y el precio de `get_special_date_info`, lo que la regla permite |
| Precios (no inventar) | Los precios del grafo son datos reales del PG; un precio 0 no se pinta | bajo | C15 correcto en todas |
| Menús no publicados | La ingesta filtra `active=1` e `is_draft=0`, pero **no** `menu_public_active`. Hoy todos tienen valor 1 | latente | `menus` del restaurante 1 |
| Extras / hablar con persona | Sin datos en el grafo. El extras va por guard | ninguno | C12/C13 iguales |

## 3. Prueba A/B (desktop)

**Montaje**
- Base de datos: `pg_dump` de solo lectura del PG de producción de las 09:08, restaurado en `vc-sa-e-pg` (sin red, auth trust).
- Proveedores invalidados en esa copia: `uazapi_servers`, tokens de instancias, `restaurant_integrations`, SMTP, Stripe, claves de IA en BD, Bunny e imagen IA.
- Backend local con `WA_OUTBOUND_BLOCK_ALL=1` (log `decision=block` en cada envío; 0 `allow`). Solo se copió la clave de MiniMax, en un fichero 0600.
- Parche local de traza, no commiteado: registra las tool calls, la cola del prompt y el payload bloqueado, y responde OK falso a los envíos bloqueados para que el bot no reintente.
- Grafo: neo4j-rust d509e4e en loopback (27300-27303), ingesta de los restaurantes 1 y 343.
- Pipeline DSPy no disponible en local: el turno va por los guards legacy, igual en las tres variantes.
- El 343 no tiene `whatsapp_pack` en producción (responde `NEEDS_SUBSCRIPTION`, el bot no contesta). Se le dio en la copia para poder probar los defaults.

**Ejecuciones:** cada caso en una conversación nueva con un remitente ficticio. OFF = `VC_GRAPHRAG=0`; ON-antes = binario base con `VC_GRAPHRAG=1`; ON-después = este PR con `VC_GRAPHRAG=1`. La ronda completa se hizo una vez; C03, C04, C05, C10 y C11 se repitieron (2 muestras). MiniMax no es determinista, así que estos resultados son orientativos.

Leyenda: ✅ cumple · ⚠️ parcial · ❌ incumple · 🛡 resuelto por guard antes del modelo (el grafo no interviene).

| Caso | Norma | Mensaje (resumen) | OFF | ON antes | ON después |
|---|---|---|---|---|---|
| C01 | menú cerrado | sin menú, platos sueltos a la carta | ✅ | ✅ (4 cafés como «reglas») | ✅ |
| C02 | menú cerrado | solo una paella sin menú | ✅ | ✅ | ✅ |
| C03 | niños | menú infantil / más barato | ✅ ✅ | ✅ ✅ | ✅ ✅ |
| C04 | arroces | 4 pax, paella + arroz negro | ✅ ✅ | ❌ ❌ (omite la variedad única) | ⚠️ ✅ |
| C05 | arroces | 6 pax Menú Arrocero, seco + meloso | ❌ ⚠️ (sigue el comentario del menú) | ❌ ❌ | ✅ ❌ (conflicto de datos, ver pendientes) |
| C06 | arroces | arroz con bogavante | ⚠️ (deriva un «encargo especial») | ✅ | ✅ |
| C07 | arroz/encargo | recoger paella mañana sin reserva | ❌ (ofrece el martes, que está cerrado) | ✅ | ✅ |
| C08 | mismo día | reservar hoy | 🛡 | 🛡 | 🛡 |
| C09 | alérgenos | «¿la paella lleva gluten?» | 🛡 | 🛡 | 🛡 |
| C10 | alérgenos | ¿sulfitos o marisco en la paella? | ⚠️ ✅ | ❌ ❌ (afirma si lleva o no sulfitos) | ⚠️ ✅ (sin alérgenos; deriva a cocina) |
| C11 | alérgenos/aptos | arroces «sin problema» para quien no come pescado | ❌ ❌ | ❌ ❌ | ❌ ❌ |
| C12 | extras | añadir bebida ilimitada | 🛡 | 🛡 | 🛡 |
| C13 | persona | hablar con alguien | ✅ | ✅ | ✅ |
| C14 | disponibilidad | mesa para 6 el sábado 21 14:30 | ✅ | ✅ | ✅ |
| C15 | precios | menú del día y de fin de semana | ✅ | ✅ | ✅ |
| C16 | horario | horario / ¿lunes noche? | ✅ | ✅ | ✅ |
| C17 | vinos | tintos y precio | ✅ | ✅ | ⚠️ (texto corrupto «másلين_», fallo del modelo) |
| C18 | fechas especiales | Nochevieja 10 pax por WhatsApp | 🛡 horizonte | 🛡 horizonte | 🛡 horizonte + info del menú (permitida) |
| C19 | 343 menú (defaults) | qué menú y precio | ✅ | ✅ | ✅ |
| C20 | 343 menú cerrado (defaults) | comer a la carta | ✅ | ✅ | ⚠️ (menciona «menú del día / fin de semana» del texto default) |

## 4. Arreglo (este PR, `graphrag_rules_precedence_v1`)
- `renderBotSystemPrompt` separa los chunks `graph:` (`botSplitGraphKnowledge`):
  - las reglas siguen en `## REGLAS APLICABLES A ESTE MENSAJE`;
  - los datos del grafo van al final del prompt, después de la ruta del pipeline y de las instrucciones del restaurante, en `## DATOS DE LA CARTA (referencia, no son reglas)`, precedidos de la línea de precedencia: mandan las reglas; disponibilidad, horarios y reservas siempre por herramientas; no sirven para confirmar ingredientes ni alérgenos.
- Fuera del grupo privilegiado se quita la parte `Alérgenos: …` de cada snippet (`botGraphBodyWithoutAllergens`). El personal del grupo privilegiado la conserva y además tiene `get_dish_allergens`.
- Con `VC_GRAPHRAG=0` no hay chunks `graph:` y el prompt sale idéntico.

## 5. Pendiente / riesgo
- **C11 (todas las variantes):** el modelo recomienda arroces como «seguros» o «sin problema» para restricciones alimentarias. No depende del grafo. La regex del guard de alérgenos no cubre «no come pescado/marisco», «sulfitos», «apto para…». Propuesta: ampliar `botAllergenIntentRe`.
- **C05 (conflicto de datos):** los comentarios de los menús de grupo («máx. 1 seco y 1 meloso por mesa», «máximo dos tipos…») contradicen la regla «una variedad en mesas de menos de 8». Jaime debe decidir cuál es la buena y alinear la regla o el comentario. Llega también por `get_menu_details`, así que no es solo cosa del grafo.
- La ingesta no filtra `menu_public_active`, y la FAQ del menú de Navidad («hay menú infantil») entra en el grafo. Ahora queda cubierto por la línea de precedencia, pero no se elimina.
- Los defaults (343) llevan texto propio de Villa Carmen («menú del día lunes a viernes / fin de semana»). Además, el 343 no tiene `whatsapp_pack` en producción.
- Muestras pequeñas (1-2 por caso) y sin el pipeline DSPy real.
