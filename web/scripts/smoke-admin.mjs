/**
 * 后台登录 + 页面冒烟测试（真实后端，开发辅助脚本）
 *
 * 前提：`npm run dev` 已启动，Go 后端在 8080 运行。
 * 通过后台登录页完成真实登录，然后遍历后台页面，检测：Art 壳渲染、
 * 横向溢出、控制台错误、空白页。
 *
 * 用法：node scripts/smoke-admin.mjs
 * 账号/密码可用环境变量覆盖：SMOKE_EMAIL / SMOKE_PASSWORD
 */
import fs from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_BASE || 'http://localhost:5173'
const EMAIL = process.env.SMOKE_EMAIL || '2304153226@qq.com'
const PASSWORD = process.env.SMOKE_PASSWORD || '2304153226@qq.com'
const ADMIN_ENTRY = process.env.SMOKE_ADMIN_ENTRY || '/admin'

const CHROME_CANDIDATES = [
  process.env.CHROME_PATH,
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
].filter(Boolean)
const executablePath = CHROME_CANDIDATES.find((p) => p && fs.existsSync(p))
if (!executablePath) {
  console.error('未找到 Chrome/Edge，请设置 CHROME_PATH')
  process.exit(1)
}

const ROUTES = [
  ['overview', '#/'],
  ['orders', '#/orders'],
  ['refunds', '#/refunds'],
  ['products', '#/products'],
  ['product-new', '#/products/new'],
  ['types', '#/types'],
  ['services', '#/services'],
  ['servers', '#/servers'],
  ['server-new', '#/servers/new'],
  ['coupons', '#/coupons'],
  ['users', '#/users'],
  ['user-edit', '#/users/1/edit'],
  ['verifications', '#/verifications'],
  ['announcements', '#/announcements'],
  ['gateway', '#/gateway'],
  ['site', '#/site'],
  ['settings-notify', '#/settings/notify'],
  ['settings-auth', '#/settings/auth'],
  ['settings-identity', '#/settings/identity'],
  ['logs', '#/logs'],
  ['totp', '#/totp'],
  ['password', '#/password'],
  ['update', '#/update'],
]

const browser = await puppeteer.launch({
  executablePath,
  headless: 'new',
  args: ['--no-sandbox', '--disable-gpu', '--hide-scrollbars'],
})
const page = await browser.newPage()
await page.setViewport({ width: 1440, height: 900 })

const consoleErrors = []
page.on('console', (msg) => {
  const t = msg.text()
  if (msg.type() === 'error' && !t.includes('Failed to load resource')) consoleErrors.push(t)
})
page.on('pageerror', (err) => consoleErrors.push('pageerror: ' + err.message))

console.log('→ 打开后台登录页…')
await page.goto(`${BASE}${ADMIN_ENTRY}#/login`, { waitUntil: 'domcontentloaded', timeout: 20000 })
await page.waitForSelector('input[autocomplete="username"]', { timeout: 10000 })
await page.type('input[autocomplete="username"]', EMAIL, { delay: 15 })
await page.type('input[autocomplete="current-password"]', PASSWORD, { delay: 15 })
await page.click('.admin-auth-submit').catch(async () => {
  await page.keyboard.press('Enter')
})

const loggedIn = await page
  .waitForSelector('.app-layout', { timeout: 20000 })
  .then(() => true)
  .catch(() => false)
console.log(loggedIn ? '→ 后台登录成功，Art 壳已渲染' : '✗ 后台登录失败：未出现 .app-layout')
if (!loggedIn) {
  await browser.close()
  process.exit(1)
}
await new Promise((r) => setTimeout(r, 1000))

const results = []

async function checkRoutes(vp) {
  for (const [name, suffix] of ROUTES) {
    consoleErrors.length = 0
    const url = `${BASE}${ADMIN_ENTRY}${suffix}`
    try {
      await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 25000 })
      await page
        .waitForFunction(() => (document.body.innerText || '').trim().length > 5, { timeout: 10000 })
        .catch(() => null)
      await new Promise((r) => setTimeout(r, 900))
      const info = await page.evaluate(() => {
        const doc = document.documentElement
        const vw = window.innerWidth
        const inClip = (el) => {
          let p = el.parentElement
          while (p && p !== doc) {
            const ox = getComputedStyle(p).overflowX
            if (ox === 'hidden' || ox === 'auto' || ox === 'scroll' || ox === 'clip') return true
            p = p.parentElement
          }
          return false
        }
        const offenders = []
        for (const el of Array.from(document.querySelectorAll('*'))) {
          const r = el.getBoundingClientRect()
          if (r.width === 0 || r.height === 0) continue
          if ((r.right > vw + 1 || r.left < -1) && !inClip(el)) {
            offenders.push(el.tagName.toLowerCase() + '.' + String(el.className).slice(0, 40))
            if (offenders.length >= 4) break
          }
        }
        return {
          overflow: doc.scrollWidth - doc.clientWidth,
          hasShell: !!document.querySelector('.app-layout'),
          textLen: (document.body.innerText || '').trim().length,
          offenders,
        }
      })
      results.push({ vp, name, url, ...info, errors: [...consoleErrors] })
      const status = info.overflow > 1 || info.offenders.length || !info.hasShell || info.textLen < 20
      console.log(
        `${status ? '✗' : '✓'} [${vp}] ${name.padEnd(13)} overflow=${info.overflow} text=${info.textLen} ${
          consoleErrors.length ? 'errors=' + consoleErrors.length : ''
        }`,
      )
      if (process.env.SMOKE_DEBUG) {
        const dbg = await page.evaluate(() => ({ href: location.href, t: (document.body.innerText || '').slice(0, 100) }))
        console.log('    dbg', JSON.stringify(dbg))
      }
    } catch (err) {
      results.push({ vp, name, url, error: String(err).slice(0, 120) })
      console.log(`✗ [${vp}] ${name.padEnd(13)} load-error: ${String(err).slice(0, 70)}`)
      if (process.env.SMOKE_DEBUG) console.log(String(err.stack).split('\n').slice(0, 4).join('\n'))
    }
  }
}

console.log('\n【桌面 1440】')
await checkRoutes('desktop')
console.log('\n【手机 390】')
await page.setViewport({ width: 390, height: 844, isMobile: true })
await checkRoutes('phone')

await browser.close()

const bad = results.filter(
  (r) =>
    r.error ||
    r.overflow > 1 ||
    (r.offenders && r.offenders.length) ||
    !r.hasShell ||
    r.textLen < 20 ||
    (r.errors && r.errors.length),
)
console.log(`\n===== 后台页面冒烟：${results.length - bad.length}/${results.length} 通过 =====`)
for (const r of bad) {
  console.log(`[${r.vp}] ${r.name} ${r.url}`)
  if (r.error) console.log('  load-error:', r.error)
  if (r.overflow > 1) console.log('  横向溢出:', r.overflow)
  if (r.offenders?.length) console.log('  offenders:', r.offenders.join(', '))
  if (r.textLen < 20) console.log('  空白页 textLen=', r.textLen)
  if (r.errors?.length) r.errors.slice(0, 3).forEach((e) => console.log('  console:', e))
}
