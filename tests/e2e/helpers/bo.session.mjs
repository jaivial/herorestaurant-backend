// Shared helpers for the beverage-options E2E suite: login to the backoffice
// JSON API and return the bo_session cookie string for WS connections.
import { rootEnv } from './vc.lib.mjs'

export function boEmail() {
  return process.env.VC_BO_EMAIL || rootEnv().BO_EMAIL || ''
}

export function boPassword() {
  return process.env.VC_BO_PASSWORD || rootEnv().BO_PASSWORD || ''
}

export function boBaseUrl() {
  return process.env.VC_BO_DEV_BASE_URL || 'https://backoffice-dev.menustudioai.com'
}

export async function boSessionCookie(correlationId) {
  const res = await fetch(`${boBaseUrl()}/api/admin/login`, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      'x-correlation-id': correlationId,
    },
    body: JSON.stringify({ identifier: boEmail(), password: boPassword() }),
  })
  const setCookie = res.headers.get('set-cookie') || ''
  const match = /bo_session=([^;]+)/i.exec(setCookie)
  if (!res.ok || !match) {
    throw new Error(`backoffice login failed: status=${res.status} hadCookie=${Boolean(match)}`)
  }
  console.log(`[${correlationId}] checkpoint bo_session_acquired`)
  return `bo_session=${match[1]}`
}
