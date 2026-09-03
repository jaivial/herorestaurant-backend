// E2E (mjs): beverage options WS protocol.
// Correlation id crosses WS envelope -> backend checkpoints -> public API payload.
import {
  assert,
  backendLogLines,
  assertBackendCheckpoints,
  createRunner,
  mysqlExec,
  mysqlRows,
  newCorrelationId,
  restaurantId,
} from './helpers/vc.lib.mjs'
import { boSessionCookie } from './helpers/bo.session.mjs'

const correlationId = newCorrelationId()
const run = createRunner(correlationId)
// Public API + WS both reached through the preact dev proxy, matching suite convention.
const apiBase = process.env.VC_PREACT_DEV_BASE_URL || 'https://preact-dev.menustudioai.com'
const wsBase = apiBase.replace(/^http/, 'ws')

console.log(`[${correlationId}] test_started scenario=beverage_options_ws_protocol`)

let menuId = null
let createdOptionId = null

function cleanup() {
  try {
    if (createdOptionId) {
      mysqlExec(`DELETE FROM restaurant_beverage_options WHERE id = ${Number(createdOptionId)}`)
    }
    if (menuId) {
      mysqlExec(`DELETE FROM menu_beverage_options WHERE menu_id = ${Number(menuId)}`)
    }
  } catch (err) {
    console.error('cleanup failed:', err && err.message)
  }
}

try {
  await run('fixture_prepared', async () => {
    const rows = mysqlRows(
      `SELECT id FROM menus WHERE restaurant_id = ${restaurantId()} AND is_draft = 0 AND active = 1 ORDER BY id DESC LIMIT 1`,
    )
    assert(rows.length === 1, 'no active menu found in dev DB for the fixture')
    menuId = Number(rows[0].id)
    assert(Number.isFinite(menuId) && menuId > 0, `invalid menu id: ${menuId}`)
    mysqlExec(`DELETE FROM menu_beverage_options WHERE menu_id = ${menuId}`)
  })

  const wsCookie = await boSessionCookie(correlationId)
  const wsURL = `${wsBase}/api/admin/group-menus-v2/ws?menuId=${menuId}`
  const ws = new WebSocket(wsURL, { headers: { cookie: wsCookie } })

  const messages = []

  function waitFor(type, timeoutMs) {
    const timeout = timeoutMs || 15000
    return new Promise((resolve, reject) => {
      const started = Date.now()
      const timer = setInterval(() => {
        for (let i = 0; i < messages.length; i++) {
          if (messages[i].type === type) {
            const found = messages.splice(i, 1)[0]
            clearInterval(timer)
            resolve(found)
            return
          }
        }
        if (Date.now() - started > timeout) {
          clearInterval(timer)
          reject(new Error(`timeout waiting for WS message type=${type}`))
        }
      }, 100)
    })
  }

  await run('ws_create_select_verify', async () => {
    await new Promise((resolve, reject) => {
      ws.on('open', resolve)
      ws.on('error', reject)
    })
    ws.on('message', (raw) => {
      try {
        messages.push(JSON.parse(String(raw)))
      } catch {
        // ignore malformed frames
      }
    })

    ws.send(JSON.stringify({ type: 'beverage_refresh', menu_id: menuId, correlation_id: correlationId }))
    const listed = await waitFor('beverage_options')
    assert(Array.isArray(listed.options) && listed.options.length > 0, 'beverage_options list empty')
    assert(listed.correlation_id === correlationId, 'correlation id not echoed on WS frame')

    ws.send(JSON.stringify({
      type: 'beverage_create',
      menu_id: menuId,
      name: `Horchata e2e ${correlationId.slice(-6)}`,
      correlation_id: correlationId,
    }))
    const created = await waitFor('beverage_options')
    const custom = created.options.find((option) => option.is_custom === true)
    assert(custom, 'custom beverage option was not created')
    createdOptionId = custom.id

    ws.send(JSON.stringify({
      type: 'beverage_set',
      menu_id: menuId,
      option_id: createdOptionId,
      selected: true,
      correlation_id: correlationId,
    }))
    await waitFor('beverage_options')
  })

  await run('public_payload_includes_selection', async () => {
    const res = await fetch(`${apiBase}/api/menus?id=${menuId}`, {
      headers: { 'x-correlation-id': correlationId },
    })
    assert(res.ok, `public menu api status ${res.status}`)
    const body = await res.json()
    const settings = body && body.menu && body.menu.settings
    assert(settings, 'public menu settings missing')
    assert(Array.isArray(settings.beverage_options), 'settings.beverage_options missing')
  })

  await run('ws_delete_custom_option', async () => {
    ws.send(JSON.stringify({
      type: 'beverage_delete',
      menu_id: menuId,
      option_id: createdOptionId,
      correlation_id: correlationId,
    }))
    const deleted = await waitFor('beverage_options')
    const stillThere = deleted.options.some((option) => option.id === createdOptionId)
    assert(!stillThere, 'custom option still listed after delete')
    createdOptionId = null
    ws.close()
  })

  await run('backend_checkpoints_verified', async () => {
    assertBackendCheckpoints(backendLogLines(correlationId), [
      'menu_beverage_ws_message_received',
      'menu_beverage_custom_created',
      'menu_beverage_persisted',
      'menu_beverage_custom_deleted',
    ])
  })

  cleanup()
  console.log(`[${correlationId}] test_completed result=passed`)
  process.exit(0)
} catch (err) {
  console.log(`[${correlationId}] test_completed result=failed error=${JSON.stringify(err && err.message)}`)
  console.error(err)
  try { ws.close() } catch { /* ignore */ }
  cleanup()
  process.exit(1)
}
