/**
 * 客户业务链路只读冒烟（真实后端，开发辅助脚本）
 *
 * 安全保证：只做页面渲染与布局检查，不提交下单/支付/续费/升级等写操作。
 * 流程：登录 → 从目录取一个真实产品 → 从服务列表取一个真实服务 →
 *       （若有）取一张可支付发票 → 逐页检查渲染/溢出/控制台错误。
 *
 * 用法：node scripts/smoke-flows.mjs
 * 账号可用 SMOKE_EMAIL / SMOKE_PASSWORD 覆盖。
 */
import fs from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_BASE || 'http://localhost:5173'
const EMAIL = process.env.SMOKE_EMAIL || '2304153226@qq.com'
const PASSWORD = process.env.SMOKE_PASSWORD || '2304153226@qq.com'

const CHROME = [
  process.env.CHROME_PATH,
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
]
  .filter(Boolean)
  .find((p) => fs.existsSync(p))
if (!CHROME) {
  console.error('未找到 Chrome/Edge，请设置 CHROME_PATH')
  process.exit(1)
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', args: ['--no-sandbox', '--disable-gpu', '--hide-scrollbars'] })
const page = await browser.newPage()
await page.setViewport({ width: 1440, height: 900 })

const consoleErrors = []
page.on('console', (m) => {
  if (m.type() === 'error' && !m.text().includes('Failed to load resource')) consoleErrors.push(m.text())
})
page.on('pageerror', (e) => consoleErrors.push('pageerror: ' + e.message))

// 1) 登录
await page.goto(BASE + '/login', { waitUntil: 'domcontentloaded', timeout: 20000 })
await page.waitForSelector('input[autocomplete="username"]', { timeout: 10000 })
await page.type('input[autocomplete="username"]', EMAIL, { delay: 10 })
await page.type('input[autocomplete="current-password"]', PASSWORD, { delay: 10 })
await page.keyboard.press('Enter')
await page.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 20000 }).catch(() => null)
await sleep(1200)
console.log('→ 已登录:', await page.evaluate(() => !!document.querySelector('.public-user__balance')))

// 2) 发现真实 ID（只读）
const sleep2 = (ms) => new Promise((r) => setTimeout(r, ms))

async function findBuyId() {
  await page.goto(BASE + '/cart', { waitUntil: 'domcontentloaded', timeout: 20000 })
  await sleep2(1500)
  const catHrefs = await page.evaluate(() =>
    Array.from(document.querySelectorAll('a.catalog-nav__parent, a.catalog-nav__child'))
      .map((a) => a.getAttribute('href'))
      .filter(Boolean),
  )
  const tried = new Set()
  for (const href of ['/cart', ...catHrefs]) {
    if (tried.has(href)) continue
    tried.add(href)
    await page.goto(BASE + href, { waitUntil: 'domcontentloaded', timeout: 20000 })
    await sleep2(1200)
    const buy = await page.evaluate(() => document.querySelector('a[href^="/buy/"]')?.getAttribute('href') || '')
    if (buy) return (buy.match(/\/buy\/(\d+)/) || [])[1] || ''
  }
  return ''
}

async function findPayId() {
  await page.goto(BASE + '/user/invoices', { waitUntil: 'domcontentloaded', timeout: 20000 })
  await sleep2(1500)
  const clicked = await page.evaluate(() => {
    const btn = Array.from(document.querySelectorAll('button')).find((b) => /去支付/.test(b.textContent || ''))
    if (!btn) return false
    btn.click()
    return true
  })
  if (!clicked) return ''
  await page.waitForFunction(() => /\/pay\/\d+/.test(location.pathname), { timeout: 8000 }).catch(() => null)
  await sleep2(600)
  return (await page.evaluate(() => location.pathname)).match(/\/pay\/(\d+)/)?.[1] || ''
}

const buyId = await findBuyId()
console.log('→ 产品:', buyId || '(所有分类均无上架产品)')
const payId = await findPayId()
console.log('→ 可支付发票:', payId || '(未找到)')

// 服务详情通过 router.push，点击列表首项获取真实 id
let serviceId = ''
await page.goto(BASE + '/services', { waitUntil: 'domcontentloaded', timeout: 20000 })
await sleep2(1500)
if (await page.$('.svc-name a')) {
  await Promise.all([
    page.waitForFunction(() => /^\/services\/\d+/.test(location.pathname), { timeout: 10000 }).catch(() => null),
    page.click('.svc-name a'),
  ])
  await sleep2(800)
  serviceId = (await page.evaluate(() => location.pathname)).match(/\/services\/(\d+)/)?.[1] || ''
}
console.log('→ 服务:', serviceId || '(该账号暂无服务)')

const ROUTES = []
if (buyId) ROUTES.push(['buy', `/buy/${buyId}`])
if (serviceId) ROUTES.push(['service-detail', `/services/${serviceId}`])
if (serviceId) ROUTES.push(['service-upgrade', `/services/${serviceId}/upgrade`])
if (payId) ROUTES.push(['pay', `/pay/${payId}`])

const results = []
async function check(vp, name, path) {
  consoleErrors.length = 0
  try {
    await page.goto(BASE + path, { waitUntil: 'domcontentloaded', timeout: 25000 })
    await page.waitForFunction(() => (document.body.innerText || '').trim().length > 5, { timeout: 10000 }).catch(() => null)
    await sleep(900)
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
      return { overflow: doc.scrollWidth - doc.clientWidth, hasShell: !!document.querySelector('.public-shell'), textLen: (document.body.innerText || '').trim().length, offenders }
    })
    results.push({ vp, name, path, ...info, errors: [...consoleErrors] })
    const bad = info.overflow > 1 || info.offenders.length || !info.hasShell || info.textLen < 20
    console.log(`${bad ? '✗' : '✓'} [${vp}] ${name.padEnd(16)} overflow=${info.overflow} text=${info.textLen} ${consoleErrors.length ? 'errors=' + consoleErrors.length : ''}`)
  } catch (err) {
    results.push({ vp, name, path, error: String(err).slice(0, 120) })
    console.log(`✗ [${vp}] ${name.padEnd(16)} load-error: ${String(err).slice(0, 70)}`)
  }
}

if (!ROUTES.length) {
  console.log('没有可检查的业务链路（可能无产品/服务）。')
} else {
  console.log('\n【桌面 1440】')
  for (const [n, p] of ROUTES) await check('desktop', n, p)
  console.log('\n【手机 390】')
  await page.setViewport({ width: 390, height: 844, isMobile: true })
  for (const [n, p] of ROUTES) await check('phone', n, p)
}

await browser.close()

const bad = results.filter((r) => r.error || r.overflow > 1 || (r.offenders && r.offenders.length) || !r.hasShell || r.textLen < 20 || (r.errors && r.errors.length))
console.log(`\n===== 业务链路只读冒烟：${results.length - bad.length}/${results.length} 通过 =====`)
for (const r of bad) {
  console.log(`[${r.vp}] ${r.name} ${r.path}`)
  if (r.error) console.log('  load-error:', r.error)
  if (r.overflow > 1) console.log('  横向溢出:', r.overflow)
  if (r.offenders?.length) console.log('  offenders:', r.offenders.join(', '))
  if (r.textLen < 20) console.log('  空白页 textLen=', r.textLen)
  if (r.errors?.length) r.errors.slice(0, 3).forEach((e) => console.log('  console:', e))
}
