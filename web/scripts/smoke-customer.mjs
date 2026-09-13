/**
 * 客户侧登录 + 页面冒烟测试（真实后端，开发辅助脚本）
 *
 * 前提：`npm run dev` 已启动，且 Go 后端在 8080 运行（Vite 代理 /__api）。
 * 通过 UI 完成真实登录，然后遍历客户端页面，检测：渲染、横向溢出、控制台错误。
 *
 * 用法：node scripts/smoke-customer.mjs
 * 账号/密码可用环境变量覆盖：SMOKE_EMAIL / SMOKE_PASSWORD
 */
import fs from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_BASE || 'http://localhost:5173'
const EMAIL = process.env.SMOKE_EMAIL || '2304153226@qq.com'
const PASSWORD = process.env.SMOKE_PASSWORD || '2304153226@qq.com'

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
  ['home', '/'],
  ['catalog', '/cart'],
  ['user-home', '/user'],
  ['services', '/services'],
  ['recharge', '/user/recharge'],
  ['invoices', '/user/invoices'],
  ['notifications', '/notifications'],
  ['profile', '/user/profile'],
  ['password', '/user/password'],
  ['verification', '/user/verification'],
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

console.log('→ 打开登录页…')
await page.goto(BASE + '/login', { waitUntil: 'domcontentloaded', timeout: 20000 })
await page.waitForSelector('input[autocomplete="username"]', { timeout: 10000 })
await page.type('input[autocomplete="username"]', EMAIL, { delay: 15 })
await page.type('input[autocomplete="current-password"]', PASSWORD, { delay: 15 })
await Promise.all([
  page.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 20000 }).catch(() => null),
  page.click('button[type="submit"]').catch(async () => {
    // 登录按钮可能是 el-button（非 submit），回退按回车提交
    await page.keyboard.press('Enter')
  }),
])
await new Promise((r) => setTimeout(r, 1200))

const afterLogin = await page.evaluate(() => ({
  path: location.pathname,
  hasAccount: !!document.querySelector('.public-user__balance'),
  balance: document.querySelector('.public-user__balance')?.textContent?.trim() || '',
}))
console.log('→ 登录后：', JSON.stringify(afterLogin))
if (!afterLogin.hasAccount) {
  console.log('✗ 登录失败：未检测到账户区（.public-user__balance）')
}

const results = []

async function checkRoutes(vp) {
  for (const [name, path] of ROUTES) {
    consoleErrors.length = 0
    try {
      await page.goto(BASE + path, { waitUntil: 'domcontentloaded', timeout: 20000 })
      // 等待 SPA 完成首次渲染（部分页面依赖接口数据）
      await page
        .waitForFunction(() => (document.body.innerText || '').trim().length > 5, { timeout: 8000 })
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
          hasShell: !!document.querySelector('.public-shell'),
          textLen: (document.body.innerText || '').trim().length,
          offenders,
        }
      })
      results.push({ vp, name, path, ...info, errors: [...consoleErrors] })
      const status = info.overflow > 1 || info.offenders.length || !info.hasShell || info.textLen < 20
      console.log(`${status ? '✗' : '✓'} [${vp}] ${name.padEnd(14)} overflow=${info.overflow} text=${info.textLen} ${consoleErrors.length ? 'errors=' + consoleErrors.length : ''}`)
    } catch (err) {
      results.push({ vp, name, path, error: String(err).slice(0, 120) })
      console.log(`✗ [${vp}] ${name.padEnd(14)} load-error: ${String(err).slice(0, 80)}`)
      if (process.env.SMOKE_DEBUG) console.log(String(err.stack).split('\n').slice(0, 5).join('\n'))
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
  (r) => r.error || r.overflow > 1 || (r.offenders && r.offenders.length) || !r.hasShell || r.textLen < 20 || (r.errors && r.errors.length),
)
console.log(`\n===== 客户页面冒烟：${results.length - bad.length}/${results.length} 通过 =====`)
for (const r of bad) {
  console.log(`[${r.vp}] ${r.name} ${r.path}`)
  if (r.error) console.log('  load-error:', r.error)
  if (r.overflow > 1) console.log('  横向溢出:', r.overflow)
  if (r.offenders?.length) console.log('  offenders:', r.offenders.join(', '))
  if (r.textLen < 20) console.log('  空白页 textLen=', r.textLen)
  if (r.errors?.length) r.errors.slice(0, 3).forEach((e) => console.log('  console:', e))
}