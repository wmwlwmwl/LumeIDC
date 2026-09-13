/**
 * 服务续费 + 升降级 端到端（真实写操作）
 *
 * ⚠️ 会真实创建账单/扣款或退款。金额上限默认 20 元（ORDER_CAP 覆盖），
 *    若产生的账单金额超过上限则不支付并中止。
 *
 * 用法：node scripts/smoke-service.mjs
 *   环境变量：SERVICE_ID（默认 29）/ UPGRADE_TARGET（默认同产品）/ SMOKE_EMAIL / SMOKE_PASSWORD
 */
import fs from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_BASE || 'http://localhost:5173'
const EMAIL = process.env.SMOKE_EMAIL || '2304153226@qq.com'
const PASSWORD = process.env.SMOKE_PASSWORD || '2304153226@qq.com'
const SID = process.env.SERVICE_ID || '29'
const CAP = Number(process.env.ORDER_CAP || 20)

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

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', args: ['--no-sandbox', '--disable-gpu'] })
const page = await browser.newPage()
await page.setViewport({ width: 1440, height: 900 })

const balance = () => page.evaluate(() => document.querySelector('.public-user__balance')?.textContent?.trim() || '')
const svcApi = (id) =>
  page.evaluate(async (i) => {
    const r = await fetch('/__api/services/' + i, { headers: { Accept: 'application/json' } })
    return r.ok ? r.json() : null
  }, id)

// 余额支付当前收银台（若跳转到 /pay/:id）
async function payIfNeeded(label) {
  const onPay = await page.waitForFunction(() => /^\/pay\/\d+/.test(location.pathname), { timeout: 12000 }).then(() => true).catch(() => false)
  if (!onPay) return { onPay: false }
  await page
    .waitForFunction(() => !!document.querySelector('.pay-amount strong') || !!document.querySelector('.el-empty'), { timeout: 15000 })
    .catch(() => null)
  await sleep(500)
  const invoiceId = (await page.evaluate(() => location.pathname)).match(/\/pay\/(\d+)/)?.[1] || ''
  const amount = parseFloat((await page.evaluate(() => document.querySelector('.pay-amount strong')?.textContent || '')).replace(/[^\d.]/g, '')) || 0
  console.log(`   ${label}: 账单 #${invoiceId} 金额 ￥${amount}`)
  if (amount > CAP) {
    console.log(`   ✗ 账单金额超过上限 ￥${CAP}，停止支付（账单 #${invoiceId} 未支付）`)
    return { onPay: true, invoiceId, paid: false, overCap: true }
  }
  if (!(await page.$('.pay-balance button'))) {
    console.log('   ✗ 不支持余额支付，停止')
    return { onPay: true, invoiceId, paid: false }
  }
  await page.click('.pay-balance button')
  const paid = await page
    .waitForFunction(() => location.pathname.startsWith('/services') || !!document.querySelector('.pay-paid'), { timeout: 25000 })
    .then(() => true)
    .catch(() => false)
  await sleep(1200)
  if (paid) {
    console.log('   余额支付成功')
  } else {
    const msgs = await page.evaluate(() => Array.from(document.querySelectorAll('.el-message')).map((m) => (m.textContent || '').trim()))
    console.log('   ✗ 支付未成功' + (msgs.length ? '：' + msgs.join(' | ') : '（无前端提示，可能后端报错）'))
  }
  return { onPay: true, invoiceId, paid }
}

// 1) 登录
await page.goto(BASE + '/login', { waitUntil: 'domcontentloaded', timeout: 20000 })
await page.waitForSelector('input[autocomplete="username"]', { timeout: 10000 })
await page.type('input[autocomplete="username"]', EMAIL, { delay: 10 })
await page.type('input[autocomplete="current-password"]', PASSWORD, { delay: 10 })
await page.keyboard.press('Enter')
await page.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 20000 }).catch(() => null)
await sleep(1200)
const balanceBefore = await balance()
const before = await svcApi(SID)
console.log(`→ 已登录，余额 ${balanceBefore}`)
console.log(`→ 服务 #${SID}: ${before?.svc?.name} 到期 ${before?.svc?.expires_at} 月价 ￥${before?.svc?.amount}`)
const expiryBefore = before?.svc?.expires_at || ''

// 2) 续费（服务列表弹窗）
console.log('\n【续费】')
if (process.env.SKIP_RENEW) {
  console.log('   （SKIP_RENEW=1，跳过续费）')
} else {
await page.goto(BASE + '/services', { waitUntil: 'domcontentloaded', timeout: 20000 })
await page.waitForSelector('.el-table__row', { timeout: 15000 }).catch(() => null)
await sleep(1200)
const rowExp = expiryBefore
const opened = await page.evaluate((exp) => {
  const rows = Array.from(document.querySelectorAll('.el-table__row'))
  const row = rows.find((r) => r.textContent.includes(exp))
  if (!row) return false
  const btn = Array.from(row.querySelectorAll('button')).find((b) => /续费/.test(b.textContent || ''))
  if (!btn) return false
  btn.click()
  return true
}, rowExp)
if (!opened) {
  console.log('✗ 未能在服务列表定位到 #' + SID + '（到期 ' + rowExp + '）的续费按钮')
} else {
  await page.waitForSelector('.el-dialog button', { timeout: 8000 }).catch(() => null)
  await sleep(400)
  const confirmed = await page.evaluate(() => {
    const dlg = Array.from(document.querySelectorAll('.el-dialog')).find((d) => /续费服务/.test(d.textContent || ''))
    if (!dlg) return false
    const btn = Array.from(dlg.querySelectorAll('button')).find((b) => /确认续费/.test(b.textContent || ''))
    if (!btn) return false
    btn.click()
    return true
  })
  if (!confirmed) console.log('✗ 未找到续费确认按钮')
  else {
    const r = await payIfNeeded('续费')
    if (!r.onPay) {
      console.log('   续费直接完成（无需支付）')
    }
    await sleep(1000)
    const afterRenew = await svcApi(SID)
    console.log('   → 到期: ' + expiryBefore + ' → ' + (afterRenew?.svc?.expires_at || '?'))
  }
}
}

// 3) 升降级
console.log('\n【升降级】')
await page.goto(BASE + `/services/${SID}/upgrade`, { waitUntil: 'domcontentloaded', timeout: 20000 })
await page.waitForSelector('.su-panel', { timeout: 15000 }).catch(() => null)
await sleep(1000)
const hasTargets = await page.evaluate(() => !!document.querySelector('.su-target-row .el-select'))
if (!hasTargets) {
  console.log('✗ 该服务不支持升降级（无目标套餐）')
} else {
  // 选择第一个目标套餐（真实鼠标点击），加载配置
  await page.click('.su-target-row .el-select')
  await page.waitForSelector('.el-select-dropdown__item:not(.is-disabled)', { timeout: 8000 }).catch(() => null)
  await sleep(300)
  const targetItems = await page.$$('.el-select-dropdown__item:not(.is-disabled)')
  const TARGET_NAME = process.env.UPGRADE_TARGET_NAME || 'cdn'
  let chosen = null
  for (const it of targetItems) {
    const t = await it.evaluate((e) => (e.textContent || '').trim())
    if (t.includes(TARGET_NAME)) {
      chosen = it
      break
    }
  }
  if (chosen || targetItems[0]) await (chosen || targetItems[0]).click()
  await sleep(400)
  const selected = await page.evaluate(() => document.querySelector('.su-target-row .el-select__selected-item, .su-target-row .el-select__placeholder')?.textContent?.trim() || '')
  console.log('   → 已选目标:', selected || '(空)')
  await page.click('.su-target-row button')
  const targetLoaded = await page
    .waitForSelector('.su-summary__diff', { timeout: 12000 })
    .then(() => true)
    .catch(() => false)
  await sleep(700)
  if (!targetLoaded) console.log('   ✗ 目标配置未加载（可能未选择成功）')
  const readInfo = () =>
    page.evaluate(() => ({
      target: document.querySelector('.su-configs') ? 'loaded' : '',
      diff: parseFloat((document.querySelector('.su-summary__diff strong')?.textContent || '').replace(/[^\d.]/g, '')) || 0,
      up: !!document.querySelector('.su-summary__diff.is-up'),
      submit: document.querySelector('.su-submit')?.textContent?.trim() || '',
    }))
  let info = await readInfo()
  console.log(`   → 配置已加载: ${!!info.target} | 差价 ￥${info.diff} (${info.up ? '需补差价/升级' : '退回差价/降级'})`)
  // 默认目标可能等价（差价 0）：调高第一个下拉配置以产生真实升级
  if (info.diff <= 0) {
    const select = await page.$('.su-configs .el-select')
    if (select) {
      await select.click()
      await page.waitForSelector('.el-select-dropdown__item', { timeout: 6000 }).catch(() => null)
      await sleep(300)
      const changed = await page.evaluate(() => {
        const items = Array.from(document.querySelectorAll('.el-select-dropdown__item'))
        if (items.length < 2) return false
        items[items.length - 1].click()
        return true
      })
      if (changed) {
        await sleep(600)
        info = await readInfo()
        console.log(`   → 调整配置后: 差价 ￥${info.diff} (${info.up ? '升级' : '降级'})`)
      }
    }
  }
  if (info.up && info.diff > CAP) {
    console.log(`   ✗ 补差价 ￥${info.diff} 超过上限 ￥${CAP}，中止。`)
  } else {
    await page.click('.su-submit')
    // 确认弹窗（ElMessageBox）
    const boxAppeared = await page
      .waitForSelector('.el-message-box__btns .el-button--primary', { timeout: 8000 })
      .then(() => true)
      .catch(() => false)
    console.log('   确认弹窗出现:', boxAppeared)
    if (boxAppeared) {
      await page.click('.el-message-box__btns .el-button--primary')
      await sleep(1500)
    }
    const notices = await page.evaluate(() => Array.from(document.querySelectorAll('.el-message')).map((m) => (m.textContent || '').trim()))
    if (notices.length) console.log('   页面提示:', notices.join(' | '))
    const r = await payIfNeeded('升级')
    if (!r.onPay) {
      await sleep(1200)
      const after = await page.evaluate(() => Array.from(document.querySelectorAll('.el-message')).map((m) => (m.textContent || '').trim()))
      console.log('   升级结果:', after.join(' | ') || '(无提示，可能未提交)')
    }
    await sleep(1000)
    const afterUp = await svcApi(SID)
    console.log('   → 服务当前: ' + JSON.stringify({ product_id: afterUp?.svc?.product_id, name: afterUp?.svc?.name, configs: afterUp?.svc?.configs, expires: afterUp?.svc?.expires_at }))
  }
}

const balanceAfter = await balance().catch(() => '')
console.log('\n→ 余额:', balanceBefore, '→', balanceAfter || '(已跳转)')
await browser.close()
