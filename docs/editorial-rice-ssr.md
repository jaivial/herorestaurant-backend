# Arroces activos en landings editoriales

Las rutas `/arroces-y-paellas` y `/guia-arroces-valencianos`, en español e inglés, incluyen los arroces de los menús 41 (día), 42 (fin de semana) y 1 (arrocero) del restaurante 1. Solo aparecen menús con `active=1` y `menu_public_active=1`; se omiten secciones sin platos y se respetan traducciones, descripciones habilitadas y suplementos.

## Prerender y revalidación

El build de `preactvillacarmen` genera un fallback SSR completo. El backend identifica sus marcadores `vc-active-rice-menus` y sustituye ese bloque y `window.__VILLACARMEN_DATA__.menus` con los datos actuales. Reutiliza el handler público de menús, por lo que no existe una segunda fuente de datos ni otra lógica de traducción.

Cada una de las cuatro URLs se guarda en una caché de proceso durante **60 segundos**. Al caducar se vuelven a leer los menús y el HTML estático. La respuesta incluye `Cache-Control: public, max-age=60, stale-while-revalidate=300`, por lo que Bunny puede revalidar en segundo plano durante otros cinco minutos. Un cambio en backoffice aparece sin redeployar ni reconstruir, normalmente en un minuto y, como máximo, dentro de la ventana de caché del CDN. `X-Villacarmen-Rice-Cache` informa de `MISS`, `HIT` o `STALE`.

Si falla la base de datos se sirve la última versión correcta de esa URL. Si todavía no existe, se sirve el fallback del último build. Un reinicio solo vacía la caché de proceso; no borra contenido.

## Pruebas sin modificar producción

`internal/api/editorial_rice_ssr_test.go` usa un directorio estático y menús simulados. Activa y desactiva el menú de fin de semana entre peticiones, comprueba ES/EN, bootstrap, caché, HEAD, rutas no relacionadas y respuesta stale ante errores. No necesita ni modifica datos del restaurante real.
