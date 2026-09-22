/**
 * 插件功能冒烟测试（真实后端 + 真实浏览器，开发辅助脚本）
 *
 * 覆盖 violation / refund 两个插件的全部页面：
 *   前台：/plugin/violation（违规公示）、/plugin/refund（申请退款）
 *   后台：/admin#/plugin/violation（违规列表 / 添加违规 / 公告设置）、/admin#/plugin/refund（退款审核）
 * 检查项：页面渲染、控制台错误、横向溢出、tab 切换、端到端写流程（后台添加违规→前台公示可见→删除清理）。
 *
 * 前提：`npm run dev` 已启动，Go 后端在 8080 运行。
 * 用法：node scripts/smoke-plugins.mjs
 * 账号可用 SMOKE_EMAIL / SMOKE_PASSWORD 覆盖；截图输出到 scripts/.smoke-shots/。
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_BASE || 'http://localhost:5173'
const EMAIL = process.env.SMOKE_EMAIL || 'smoke@lumeidc.local'
const PASSWORD = process.env.SMOKE_PASSWORD || 'Smoke@12345'
const ADMIN_ACCOUNT = process.env.SMOKE_ADMIN || 'smokeqa'
const ADMIN_PASSWORD = process.env.SMOKE_ADMIN_PASSWORD || 'Smoke@12345'
const SHOTS = path.join(path.dirname(fileURLToPath(import.meta.url)), '.smoke-shots')
fs.mkdirSync(SHOTS, { recursive: true })

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
const DESKTOP = { width: 1440, height: 900 }
const MOBILE = { width: 375, height: 667 }

const results = []
function record(name, pass, detail) {
  results.push({ name, pass, detail })
  console.log(`${pass ? '✓' : '✗'} ${name}${detail ? ' — ' + detail : ''}`)
}

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  args: ['--no-sandbox', '--disable-gpu', '--hide-scrollbars'],
})

// 前台/后台各用独立 cookie 上下文，避免互相顶掉登录态
const createCtx = browser.createBrowserContext
  ? () => browser.createBrowserContext()
  : () => browser.createIncognitoBrowserContext()

function attachErrorCollector(page) {
  const errors = []
  page.on('console', (m) => {
    if (m.type() === 'error' && !m.text().includes('Failed to load resource')) errors.push(m.text())
  })
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message))
  return errors
}

// 本地图形验证码是固定 5x7 点阵字体、位置确定，可直接在页面内解码。
// 仅在后台因登录失败被强制出验证码时才会用到（正常路径不会出现）。
const CAPTCHA_GLYPHS = {
  2: [6, 9, 1, 2, 4, 8, 15], 3: [14, 1, 1, 6, 1, 1, 14], 4: [2, 6, 10, 10, 15, 2, 2], 5: [15, 8, 8, 14, 1, 1, 14],
  6: [6, 8, 8, 14, 9, 9, 6], 7: [15, 1, 2, 4, 4, 4, 4], 8: [6, 9, 9, 6, 9, 9, 6], 9: [6, 9, 9, 7, 1, 1, 6],
  A: [6, 9, 9, 15, 9, 9, 9], B: [14, 9, 9, 14, 9, 9, 14], C: [6, 9, 8, 8, 8, 9, 6], D: [14, 9, 9, 9, 9, 9, 14],
  E: [15, 8, 8, 14, 8, 8, 15], F: [15, 8, 8, 14, 8, 8, 8], G: [6, 9, 8, 11, 9, 9, 7], H: [9, 9, 9, 15, 9, 9, 9],
  J: [1, 1, 1, 1, 1, 9, 6], K: [9, 10, 12, 12, 10, 10, 9], L: [8, 8, 8, 8, 8, 8, 15], M: [9, 15, 15, 9, 9, 9, 9],
  N: [9, 13, 15, 11, 9, 9, 9], P: [14, 9, 9, 14, 8, 8, 8], Q: [6, 9, 9, 9, 10, 5, 7], R: [14, 9, 9, 14, 10, 9, 9],
  S: [7, 8, 8, 6, 1, 1, 14], T: [15, 2, 2, 2, 2, 2, 2], U: [9, 9, 9, 9, 9, 9, 6], V: [9, 9, 9, 9, 9, 9, 6],
  W: [9, 9, 9, 15, 15, 15, 6], X: [9, 9, 6, 6, 6, 9, 9], Y: [9, 9, 6, 6, 2, 2, 2], Z: [15, 1, 2, 4, 8, 8, 15],
}

async function solveCaptchaIfPresent(page) {
  const input = await page.$('input[placeholder="请输入图中字符"]')
  if (!input) return false
  const answer = await page.evaluate((glyphs) => {
    const img = document.querySelector('.admin-login__captcha-refresh img')
    if (!img || !img.src) return null
    const canvas = document.createElement('canvas')
    const ctx = canvas.getContext('2d')
    canvas.width = img.naturalWidth || 150
    canvas.height = img.naturalHeight || 48
    ctx.drawImage(img, 0, 0)
    const px = ctx.getImageData(0, 0, canvas.width, canvas.height).data
    const on = (x, y) => {
      const i = (y * canvas.width + x) * 4
      return px[i] < 150 && px[i + 1] < 150 && px[i + 2] > 100
    }
    let out = ''
    for (let i = 0; i < 5; i++) {
      const x0 = 12 + i * 27
      const rows = []
      for (let y = 0; y < 7; y++) {
        let row = 0
        for (let x = 0; x < 4; x++) if (on(x0 + x * 4 + 2, 8 + y * 4 + 2)) row |= 1 << (3 - x)
        rows.push(row)
      }
      let best = '?'
      let bestScore = -1
      for (const [ch, g] of Object.entries(glyphs)) {
        let score = 0
        for (let y = 0; y < 7; y++) {
          for (let x = 0; x < 4; x++) if (((rows[y] >> (3 - x)) & 1) === ((g[y] >> (3 - x)) & 1)) score++
        }
        if (score > bestScore) {
          bestScore = score
          best = ch
        }
      }
      out += best
    }
    return out
  }, CAPTCHA_GLYPHS)
  if (!answer || answer.includes('?')) return false
  await page.type('input[placeholder="请输入图中字符"]', answer, { delay: 20 })
  return true
}

async function loginCustomer(page) {
  await page.goto(BASE + '/login', { waitUntil: 'domcontentloaded', timeout: 20000 })
  await page.waitForSelector('input[autocomplete="username"]', { timeout: 10000 })
  await page.type('input[autocomplete="username"]', EMAIL, { delay: 10 })
  await page.type('input[autocomplete="current-password"]', PASSWORD, { delay: 10 })
  await page.keyboard.press('Enter')
  await page.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 20000 }).catch(() => null)
  await sleep(1500)
}

async function loginAdmin(page) {
  await page.goto(BASE + '/admin#/login', { waitUntil: 'domcontentloaded', timeout: 20000 })
  await page.waitForSelector('input[autocomplete="username"]', { timeout: 10000 })
  await page.type('input[autocomplete="username"]', ADMIN_ACCOUNT, { delay: 15 })
  await page.type('input[autocomplete="current-password"]', ADMIN_PASSWORD, { delay: 15 })
  await sleep(800)
  await solveCaptchaIfPresent(page)
  await page.click('.admin-login__submit').catch(async () => {
    await page.keyboard.press('Enter')
  })
  const ok = await page
    .waitForSelector('.app-layout', { timeout: 20000 })
    .then(() => true)
    .catch(() => false)
  await sleep(1500)
  return ok
}

async function checkRender(page, errors, name, url, { waitSelector, waitText, shot, viewport }) {
  errors.length = 0
  if (viewport) await page.setViewport(viewport)
  try {
    await page.goto(BASE + url, { waitUntil: 'domcontentloaded', timeout: 25000 })
    if (waitSelector) await page.waitForSelector(waitSelector, { timeout: 15000 })
    if (waitText) {
      await page.waitForFunction((t) => (document.body.innerText || '').includes(t), { timeout: 15000 }, waitText)
    }
    await sleep(1500)
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    const bodyLen = await page.evaluate(() => (document.body.innerText || '').trim().length)
    if (shot) await page.screenshot({ path: path.join(SHOTS, shot) })
    const pass = bodyLen > 5 && overflow <= 1 && errors.length === 0
    record(
      name,
      pass,
      pass
        ? `正文 ${bodyLen} 字符，无横向溢出，无控制台错误`
        : `正文 ${bodyLen} 字符，横向溢出 ${overflow}px，控制台错误 ${errors.length} 条${errors.length ? '：' + errors.slice(0, 3).join(' | ') : ''}`,
    )
    return pass
  } catch (e) {
    record(name, false, '异常：' + e.message)
    return false
  }
}

async function clickTab(page, label) {
  return page.evaluate((l) => {
    const items = Array.from(document.querySelectorAll('.violation-tabs .el-tabs__item'))
    const item = items.find((i) => (i.textContent || '').trim() === l)
    if (!item) return false
    item.click()
    return true
  }, label)
}

// Element Plus 2.11 的 filterable el-select 输入框不带 placeholder 属性，
// 占位文案是独立的 .el-select__placeholder 节点，故按文案定位后打开并聚焦输入框。
async function openSelectByPlaceholder(page, placeholderText) {
  return page.evaluate((text) => {
    const isVisible = (el) => el && el.getClientRects().length > 0
    const ph = Array.from(document.querySelectorAll('.el-select__placeholder')).find(
      (p) => (p.textContent || '').trim() === text && isVisible(p),
    )
    if (!ph) return false
    const select = ph.closest('.el-select')
    const wrap = ph.closest('.el-select__wrapper') || ph
    wrap.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    const input = select && select.querySelector('.el-select__input')
    if (input) input.focus()
    return !!input
  }, placeholderText)
}

// 从当前展开的下拉里选第一个可见选项
async function pickFirstDropdownItem(page, timeout = 12000) {
  await page
    .waitForFunction(
      () =>
        Array.from(document.querySelectorAll('.el-select-dropdown__item')).some((i) => i.getClientRects().length > 0),
      { timeout },
    )
    .catch(() => null)
  await sleep(400)
  return page.evaluate(() => {
    const vis = Array.from(document.querySelectorAll('.el-select-dropdown__item')).filter(
      (i) => i.getClientRects().length > 0,
    )
    if (!vis.length) return ''
    vis[0].click()
    return vis[0].textContent.trim()
  })
}

// ==================== 后台（先登录，先造数据） ====================
const adminCtx = await createCtx()
const adminPage = await adminCtx.newPage()
const adminErrors = attachErrorCollector(adminPage)
// 探针：抓取所有插件 API 的响应（方法/路径/状态码/响应体片段），用于排查保存链路
const netLog = []
adminPage.on('response', async (res) => {
  const url = res.url()
  if (!/\/plugin\/(violation|refund)/.test(url)) return
  let body = ''
  try {
    body = (await res.text()).slice(0, 200).replace(/\s+/g, ' ')
  } catch {
    body = '(body 不可读)'
  }
  netLog.push(`${res.request().method()} ${res.status()} ${url.replace(BASE, '')} :: ${body}`)
})
await adminPage.setViewport(DESKTOP)
const adminLoggedIn = await loginAdmin(adminPage)
record('后台登录', adminLoggedIn, adminLoggedIn ? 'Art 壳已渲染' : '未出现 .app-layout')

await checkRender(adminPage, adminErrors, '后台-违规管理-违规列表-桌面', '/admin#/plugin/violation', {
  waitSelector: '.violation-tabs',
  waitText: '记录总数',
  shot: '01-admin-violation-list.png',
  viewport: DESKTOP,
})

for (const [label, expectText, shot] of [
  ['添加违规', '违规描述', '02-admin-violation-add.png'],
  ['公告设置', '新增公告', '03-admin-violation-announcement.png'],
  ['违规列表', '记录总数', '04-admin-violation-list-tab.png'],
]) {
  adminErrors.length = 0
  const clicked = await clickTab(adminPage, label)
  if (!clicked) {
    record(`后台-违规管理-${label}-tab`, false, '未找到该 tab')
    continue
  }
  await sleep(1500)
  const has = await page_text(adminPage, expectText)
  await adminPage.screenshot({ path: path.join(SHOTS, shot) })
  record(`后台-违规管理-${label}-tab`, has && adminErrors.length === 0, has ? `内容已渲染（含「${expectText}」）` : `未找到「${expectText}」`)
}

await checkRender(adminPage, adminErrors, '后台-退款审核-桌面', '/admin#/plugin/refund', {
  waitText: '待审核',
  shot: '05-admin-refund.png',
  viewport: DESKTOP,
})
await checkRender(adminPage, adminErrors, '后台-退款审核-移动', '/admin#/plugin/refund', {
  waitText: '退款审核',
  shot: '06-admin-refund-mobile.png',
  viewport: MOBILE,
})

// ==================== 端到端：后台添加违规（公示开启） ====================
const MARKER = `SMOKE-${Date.now().toString().slice(-6)}`
const DESC = `${MARKER} 由冒烟脚本自动创建，用于验证违规公示链路`

// 前面在退款页做过检查，必须先回到违规管理页，否则 .violation-tabs 不在 DOM 中
await adminPage.goto(BASE + '/admin#/plugin/violation', { waitUntil: 'domcontentloaded', timeout: 20000 })
await adminPage.waitForSelector('.violation-tabs', { timeout: 15000 })
await sleep(1200)
const backToAdd = await clickTab(adminPage, '添加违规')
await sleep(1800)
if (!backToAdd) record('端到端-后台添加违规', false, '未能切到「添加违规」tab')

// 违规用户：远程搜索并选中测试账号自己
let userPicked = ''
if (backToAdd) {
  const opened = await openSelectByPlaceholder(adminPage, '输入邮箱或用户名搜索')
  if (opened) {
    await adminPage.keyboard.type(EMAIL, { delay: 40 })
    userPicked = await pickFirstDropdownItem(adminPage, 12000)
  }
}

// 违规类型：有下拉选项则选第一个，否则退化为输入框
let typePicked = ''
if (backToAdd) {
  const opened = await openSelectByPlaceholder(adminPage, '选择违规类型')
  if (opened) {
    typePicked = await pickFirstDropdownItem(adminPage, 8000)
    if (!typePicked) typePicked = '下拉无选项'
  } else {
    const fallback = await adminPage.$('input[placeholder="如：滥用资源"]')
    if (fallback) {
      await adminPage.type('input[placeholder="如：滥用资源"]', '滥用资源')
      typePicked = '滥用资源（输入）'
    }
  }
}

// 违规等级：选「中度」（限定在 RecordForm 表单内，避开页面上方插件配置卡片）
let levelPicked = ''
if (backToAdd) {
  levelPicked = await adminPage.evaluate(() => {
    const form = document.querySelector('textarea[placeholder="描述违规事实、发生时间与影响范围"]')?.closest('form')
    if (!form) return ''
    const radios = Array.from(form.querySelectorAll('.el-radio'))
    const r = radios.find((i) => (i.textContent || '').trim() === '中度') || radios[0]
    if (!r) return ''
    r.click()
    return r.textContent.trim()
  })
}

// 违规描述
if (backToAdd) await adminPage.type('textarea[placeholder="描述违规事实、发生时间与影响范围"]', DESC)

// 是否公示：确保打开（同样限定在 RecordForm 内）
let publicOn = 'skipped'
if (backToAdd) {
  publicOn = await adminPage.evaluate(() => {
    const form = document.querySelector('textarea[placeholder="描述违规事实、发生时间与影响范围"]')?.closest('form')
    if (!form) return 'no-form'
    const vis = Array.from(form.querySelectorAll('.el-switch'))
    if (!vis.length) return 'no-switch'
    if (!vis[0].classList.contains('is-checked')) vis[0].click()
    return 'on'
  })
}

// 保存（捕获真实提示文案，避免把失败提示误判成成功；
// 关键：页面顶部还有插件配置卡片的「保存」按钮，必须点 RecordForm 表单内的那个）
let saveToast = ''
if (backToAdd) {
  await adminPage.evaluate(() => {
    const form = document.querySelector('textarea[placeholder="描述违规事实、发生时间与影响范围"]')?.closest('form')
    const btn = form
      ? Array.from(form.querySelectorAll('button')).find((b) => (b.textContent || '').trim() === '保存')
      : null
    btn?.click()
  })
  saveToast = await adminPage
    .waitForFunction(
      () => {
        const m = Array.from(document.querySelectorAll('.el-message')).find((x) => /已保存|失败/.test(x.textContent || ''))
        return m ? (m.textContent || '').trim() : false
      },
      { timeout: 12000 },
    )
    .then((h) => h.jsonValue())
    .catch(() => '')
}
// 探针：保存后检查表单是否被重置（成功路径会 resetForm 清空描述），及按钮是否仍在加载
const formAfter = backToAdd
  ? await adminPage.evaluate(() => ({
      desc: (document.querySelector('textarea[placeholder="描述违规事实、发生时间与影响范围"]') || {}).value || '',
      savingBtn: Array.from(document.querySelectorAll('button')).some(
        (b) => (b.textContent || '').trim() === '保存' && b.classList.contains('is-loading'),
      ),
    }))
  : { desc: '', savingBtn: false }
record(
  '端到端-后台添加违规',
  saveToast.includes('已保存'),
  saveToast
    ? `用户「${userPicked || '?'}」类型「${typePicked || '?'}」等级「${levelPicked || '?'}」公示「${publicOn}」，提示「${saveToast}」，表单${formAfter.desc ? '未重置' : '已重置'}${formAfter.savingBtn ? '，保存中' : ''}`
    : '未出现任何保存提示',
)
await adminPage.screenshot({ path: path.join(SHOTS, '07-e2e-admin-saved.png') })

// ==================== 前台（独立上下文登录后验证公示） ====================
const customerCtx = await createCtx()
const customerPage = await customerCtx.newPage()
const customerErrors = attachErrorCollector(customerPage)
// 探针：前台插件 API 的响应轨迹
const custNetLog = []
customerPage.on('response', async (res) => {
  const url = res.url()
  if (!/\/plugin\/(violation|refund)/.test(url)) return
  let body = ''
  try {
    body = (await res.text()).slice(0, 300).replace(/\s+/g, ' ')
  } catch {
    body = '(body 不可读)'
  }
  custNetLog.push(`${res.request().method()} ${res.status()} ${url.replace(BASE, '')} :: ${body}`)
})
await customerPage.setViewport(DESKTOP)
await loginCustomer(customerPage)

await checkRender(customerPage, customerErrors, '前台-违规公示-桌面', '/plugin/violation', {
  waitText: '违规公示',
  shot: '08-customer-violation.png',
  viewport: DESKTOP,
})
await checkRender(customerPage, customerErrors, '前台-违规公示-移动', '/plugin/violation', {
  waitText: '违规公示',
  shot: '09-customer-violation-mobile.png',
  viewport: MOBILE,
})
await checkRender(customerPage, customerErrors, '前台-申请退款-桌面', '/plugin/refund', {
  waitText: '申请退款',
  shot: '10-customer-refund.png',
  viewport: DESKTOP,
})
await checkRender(customerPage, customerErrors, '前台-申请退款-移动', '/plugin/refund', {
  waitText: '申请退款',
  shot: '11-customer-refund-mobile.png',
  viewport: MOBILE,
})

// 端到端验证：记录在公示表格中渲染，点「详情」弹窗显示含 MARKER 的描述
// （表格列不含描述，MARKER 只在详情里，故不能直接在页面正文找 MARKER）
await customerPage.setViewport(DESKTOP)
await customerPage.goto(BASE + '/plugin/violation', { waitUntil: 'domcontentloaded', timeout: 20000 })
let rowRendered = false
let detailOk = false
let feedDetail = ''
try {
  await customerPage.waitForSelector('.el-table__row', { timeout: 12000 })
  rowRendered = true
} catch {
  feedDetail = '公示表格未出现数据行'
}
if (rowRendered) {
  await customerPage.evaluate(() => {
    const row = document.querySelector('.el-table__row')
    const btn = row && Array.from(row.querySelectorAll('button')).find((b) => (b.textContent || '').trim() === '详情')
    btn?.click()
  })
  detailOk = await customerPage
    .waitForFunction(
      (m) => {
        const dlg = document.querySelector('.el-dialog')
        return !!dlg && !dlg.classList.contains('is-hidden') && (dlg.textContent || '').includes(m)
      },
      { timeout: 10000 },
      MARKER,
    )
    .then(() => true)
    .catch(() => false)
  feedDetail = detailOk ? '行已渲染，详情弹窗显示完整描述' : '详情弹窗未显示该记录描述'
}
await customerPage.screenshot({ path: path.join(SHOTS, '12-e2e-feed-visible.png') })
record(
  '端到端-前台公示可见',
  rowRendered && detailOk,
  `行渲染=${rowRendered}，详情含 MARKER=${detailOk}；${feedDetail}`,
)
// 关闭详情弹窗，避免干扰后续
if (detailOk) {
  await customerPage.evaluate(() => {
    const btn = Array.from(document.querySelectorAll('.el-dialog button')).find((b) => (b.textContent || '').trim() === '关闭')
    btn?.click()
  })
  await sleep(600)
}

// ==================== 清理测试记录 ====================
await adminPage.setViewport(DESKTOP)
await clickTab(adminPage, '违规列表')
await sleep(2000)
// 后台列表没有「描述」列，MARKER 只写在描述里，行文本永远匹配不到；
// 改用关键词搜索定位（后端 keyword 匹配描述，MARKER 唯一，结果只会命中测试记录）
const KW_SEL = 'input[placeholder="用户 / 类型 / 描述"]'
let searched = false
if (await adminPage.$(KW_SEL)) {
  await adminPage.click(KW_SEL, { clickCount: 3 })
  await adminPage.type(KW_SEL, MARKER, { delay: 10 })
  await adminPage.evaluate(() => {
    const btn = Array.from(document.querySelectorAll('.art-search-bar button')).find(
      (b) => (b.textContent || '').trim() === '查询' && b.getClientRects().length > 0,
    )
    btn?.click()
  })
  searched = true
  await sleep(2500)
}
const rowCount = await adminPage.evaluate(() => document.querySelectorAll('.el-table__row').length)
const delClicked = searched
  ? await adminPage.evaluate(() => {
      const rows = Array.from(document.querySelectorAll('.el-table__row'))
      if (!rows.length) return false
      const btn = Array.from(rows[0].querySelectorAll('button')).find((b) => (b.textContent || '').trim() === '删除')
      btn?.click()
      return !!btn
    })
  : false
let cleanupOk = false
if (delClicked) {
  await sleep(800)
  await adminPage.evaluate(() => {
    const box = document.querySelector('.el-message-box')
    if (!box) return
    const btn = Array.from(box.querySelectorAll('button')).find((b) => (b.textContent || '').trim() === '删除')
    btn?.click()
  })
  // 判定：行清空，或剩余行中不含 MARKER（不受其他表格/渲染行干扰）
  cleanupOk = await adminPage
    .waitForFunction(
      (m) => {
        const rows = Array.from(document.querySelectorAll('.el-table__row'))
        return rows.length === 0 || !rows.some((r) => (r.textContent || '').includes(m))
      },
      { timeout: 12000 },
      MARKER,
    )
    .then(() => true)
    .catch(() => false)
}
// 诊断信息：删除后剩余行数与首行文本片段
const afterRows = await adminPage.evaluate(() => {
  const rows = Array.from(document.querySelectorAll('.el-table__row'))
  return { count: rows.length, first: rows.length ? (rows[0].textContent || '').trim().slice(0, 80) : '' }
})
record(
  '端到端-清理测试记录',
  cleanupOk,
  cleanupOk
    ? '按关键词搜索定位并删除，记录已清理'
    : delClicked
      ? `点击删除后记录仍可见（剩余行数 ${afterRows.count}${afterRows.first ? `，首行：${afterRows.first}` : ''}）`
      : searched
        ? `关键词搜索「${MARKER}」后未出现可删除的行（行数 ${rowCount}）`
        : '未找到关键词搜索框',
)

// ==================== 汇总 ====================
const failed = results.filter((r) => !r.pass)
console.log(`\n========== 插件冒烟结果：${results.length - failed.length}/${results.length} 通过 ==========`)
if (failed.length) {
  console.log('失败项：')
  for (const f of failed) console.log(`  ✗ ${f.name} — ${f.detail}`)
}
console.log(`截图目录：${SHOTS}`)
// 请求轨迹仅在失败时输出，作为排查依据；全通过时保持输出干净
if (failed.length) {
  console.log(`\n---------- 后台插件 API 请求轨迹（共 ${netLog.length} 条） ----------`)
  for (const l of netLog) console.log('  ' + l)
  console.log(`\n---------- 前台插件 API 请求轨迹（共 ${custNetLog.length} 条） ----------`)
  for (const l of custNetLog) console.log('  ' + l)
}

await browser.close()
process.exit(failed.length ? 1 : 0)

async function page_text(page, t) {
  return page.evaluate((text) => (document.body.innerText || '').includes(text), t)
}
