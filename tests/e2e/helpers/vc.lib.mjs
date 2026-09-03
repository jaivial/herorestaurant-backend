// Shared helpers for Villa Carmen .mjs e2e tests (SAGE observable-task contract).
// Reads every credential/URL from environment files or env vars — never hardcode.
import { randomUUID } from 'node:crypto'
import { execFileSync, spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { chromium } from 'playwright'

const ROOT_ENV_PATH = process.env.VC_ROOT_ENV || '/var/www/newvillacarmen/.env'

let cachedEnv = null
export function rootEnv() {
  if (cachedEnv) return cachedEnv
  const env = {}
  for (const rawLine of readFileSync(ROOT_ENV_PATH, 'utf8').split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line || line.startsWith('#')) continue
    const eq = line.indexOf('=')
    if (eq <= 0) continue
    env[line.slice(0, eq)] = line.slice(eq + 1)
  }
  cachedEnv = env
  return env
}

// Matches DB_NAME of the backend service in docker-compose.dev.yml.
export function devDbName() {
  return process.env.VC_DEV_DB_NAME || 'newvillacarmen_dev'
}

export function baseUrl() {
  return process.env.VC_PREACT_DEV_BASE_URL || 'https://preact-dev.menustudioai.com'
}

export function backendContainer() {
  return process.env.VC_BACKEND_DEV_CONTAINER || 'newvillacarmen-backend-dev'
}

export function restaurantId() {
  return process.env.VC_DEV_RESTAURANT_ID || rootEnv().DEFAULT_RESTAURANT_ID || '1'
}

export function newCorrelationId() {
  return `e2e-menusdegrupos-${randomUUID()}`
}

export function assert(cond, msg) {
  if (!cond) throw new Error(`assertion failed: ${msg}`)
}

export function sqlQuote(value) {
  return `'${String(value).replace(/\\/g, '\\\\').replace(/'/g, "\\'")}'`
}

function mysqlRaw(sql) {
  const env = rootEnv()
  return execFileSync(
    'mysql',
    ['-h', process.env.VC_DB_HOST || env.DB_HOST, '-P', env.DB_PORT, '-u', env.DB_USER, '-D', devDbName(), '--batch', '--raw', '-e', sql],
    { encoding: 'utf8', env: { ...process.env, MYSQL_PWD: env.DB_PASSWORD } },
  )
}

function unescapeField(s) {
  return s.replace(/\\(.)/g, (_, c) => ({ n: '\n', t: '\t', r: '\r', '0': '\0', '"': '"', '\\': '\\' }[c] ?? `\\${c}`))
}

export function mysqlRows(sql) {
  const out = mysqlRaw(sql)
  const lines = out.split('\n').filter((l) => l.length > 0)
  if (lines.length === 0) return []
  const headers = lines[0].split('\t').map(unescapeField)
  return lines.slice(1).map((line) => {
    const cells = line.split('\t').map(unescapeField)
    const row = {}
    headers.forEach((h, i) => {
      row[h] = cells[i]
    })
    return row
  })
}

export function mysqlExec(sql) {
  mysqlRaw(sql)
}

export async function withBrowserContext(correlationId, fn) {
  const browser = await chromium.launch()
  try {
    const context = await browser.newContext({
      ignoreHTTPSErrors: true,
      locale: 'es-ES',
      extraHTTPHeaders: { 'x-correlation-id': correlationId },
    })
    const page = await context.newPage()
    const consoleCheckpoints = []
    page.on('console', (msg) => {
      const m = /\[checkpoint\]\s+([a-z0-9_]+)/i.exec(msg.text())
      if (m) consoleCheckpoints.push(m[1])
    })
    await fn({ page, consoleCheckpoints })
  } finally {
    await browser.close()
  }
}

export function createRunner(correlationId) {
  return async function step(name, fn) {
    console.log(`[${correlationId}] checkpoint ${name}_started`)
    try {
      await fn()
      console.log(`[${correlationId}] checkpoint ${name}_completed`)
    } catch (err) {
      console.log(`[${correlationId}] checkpoint ${name}_failed error=${JSON.stringify(err && err.message)}`)
      throw err
    }
  }
}

export function backendLogLines(correlationId) {
  // Go's log package writes to stderr — merge both streams.
  const res = spawnSync('docker', ['logs', '--tail', '5000', backendContainer()], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  })
  const out = `${res.stdout || ''}${res.stderr || ''}`
  return out.split('\n').filter((line) => line.includes(correlationId))
}

export function assertBackendCheckpoints(lines, names) {
  const found = []
  for (const name of names) {
    const idx = lines.findIndex((l) => l.includes(`checkpoint ${name}`))
    assert(idx !== -1, `backend checkpoint "${name}" missing in ${backendContainer()} logs for correlation id`)
    found.push(idx)
  }
  for (let i = 1; i < found.length; i++) {
    assert(found[i] > found[i - 1], `backend checkpoint "${names[i]}" appears out of order`)
  }
}

// wouter listens to popstate; pushState + synthetic popstate navigates the SPA
// without a full page load (keeps the in-memory api cache alive).
export async function spaNavigate(page, path) {
  await page.evaluate((p) => {
    history.pushState({}, '', p)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, path)
}

// Console events travel over CDP asynchronously — poll briefly before asserting
// a frontend checkpoint that was emitted in the same tick as the DOM update.
export async function waitForConsoleCheckpoint(page, consoleCheckpoints, name, timeoutMs = 5000) {
  const start = Date.now()
  while (!consoleCheckpoints.includes(name)) {
    if (Date.now() - start > timeoutMs) {
      throw new Error(`assertion failed: frontend checkpoint "${name}" missing from console output`)
    }
    await page.waitForTimeout(100)
  }
}
