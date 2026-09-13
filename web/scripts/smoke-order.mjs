/**
 * 下单 → 余额支付 端到端（真实写操作）
 *
 * ⚠️ 会真实创建订单并扣减余额。金额上限默认 20 元（ORDER_CAP 覆盖），
 *    超过上限则中止。默认下单产品 ORDER_PRODUCT=20。
 *
 * 用法：node scripts/smoke-order.mjs
 *   环境变量：SMOKE_EMAIL / SMOKE_PASSWORD / ORDER_PRODUCT / ORDER_CAP
 */
import fs from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_BASE || 'http://localhost:5173'
const EMAIL = process.env.SMOKE_EMAIL || '2304153226@qq.com'
const PASSWORD = process.env.SMOKE_PASSWORD || '2304153226@qq.com'
const PRODUCT_ID = process.env.ORDER_PRODUCT || '20'
const CAP = Number(process.env.ORDER_CAP || 20)
const PAY_ONLY = process.env.PAY_ONLY || ''

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

// 1) 登录
await page.goto(BASE + '/login', { waitUntil: 'domcontentloaded', timeout: 20000 })
await page.waitForSelector('input[autocomplete="username"]', { timeout: 10000 })
await page.type('input[autocomplete="username"]', EMAIL, { delay: 10 })
await page.type('input[autocomplete="current-password"]', PASSWORD, { delay: 10 })
await page.keyboard.press('Enter')
await page.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 20000 }).catch(() => null)
await sleep(1200)
const balanceBefore = await balance()
console.log(`→ 已登录，余额: ${balanceBefore} | 产品 #${PRODUCT_ID} | 金额上限: ￥${CAP}`)

let invoiceId = PAY_ONLY

if (!PAY_ONLY) {
  // 2) 打开下单页
  await page.goto(BASE + '/buy/' + PRODUCT_ID, { waitUntil: 'domcontentloaded', timeout: 20000 })
  const loaded = await page.waitForSelector('.buy-head h1', { timeout: 15000 }).then(() => true).catch(() => false)
  if (!loaded) {
    console.log('✗ 产品页未加载（产品不存在或已下架）')
    await browser.close()
    process.exit(1)
  }
  await sleep(1000)
  const meta = await page.evaluate(() => ({
    name: document.querySelector('.buy-head h1')?.textContent?.trim() || '',
    total: parseFloat((document.querySelector('.buy-summary__total strong')?.textContent || '').replace(/[^\d.]/g, '')) || 0,
    soldout: !!document.querySelector('.buy-warn.is-red'),
    needIdentity: !!document.querySelector('.buy-warn.is-amber'),
    canBuy: !!document.querySelector('.buy-submit'),
  }))
  console.log(`→ 产品: ${meta.name} | 合计: ￥${meta.total}${meta.soldout ? ' [售罄]' : ''}${meta.needIdentity ? ' [需实名]' : ''}`)
  if (meta.soldout || !meta.canBuy) {
    console.log('✗ 不可下单（售罄或按钮缺失），终止。')
    await browser.close()
    process.exit(1)
  }
  if (meta.total > CAP) {
    console.log(`✗ 合计 ￥${meta.total} 超过上限 ￥${CAP}，按约定不下单。`)
    await browser.close()
    process.exit(2)
  }

  // 3) 下单
  await page.click('.buy-submit')
  const reachedPay = await page
    .waitForFunction(() => /^\/pay\/\d+/.test(location.pathname), { timeout: 20000 })
    .then(() => true)
    .catch(() => false)
  if (!reachedPay) {
    const msg = await page.evaluate(() => document.querySelector('.el-message')?.textContent?.trim() || '')
    console.log('✗ 未跳转到收银台，下单可能失败：', msg || '(无提示)')
    await browser.close()
    process.exit(1)
  }
  invoiceId = (await page.evaluate(() => location.pathname)).match(/\/pay\/(\d+)/)?.[1] || ''
  console.log('→ 下单成功，账单 #' + invoiceId + '，进入收银台')
} else {
  await page.goto(BASE + '/pay/' + invoiceId, { waitUntil: 'domcontentloaded', timeout: 20000 })
}

// 4) 余额支付
await page
  .waitForFunction(
    () => !!document.querySelector('.pay-amount strong') || !!document.querySelector('.pay-paid') || !!document.querySelector('.el-empty'),
    { timeout: 15000 },
  )
  .catch(() => null)
await sleep(600)
const amount = await page.evaluate(() => (document.querySelector('.pay-amount strong')?.textContent || '').trim())
const hasBalancePay = await page.evaluate(() => !!document.querySelector('.pay-balance button'))
console.log(`→ 账单 #${invoiceId} 金额: ${amount || '(空)'} | 支持余额支付: ${hasBalancePay}`)
if (!hasBalancePay) {
  console.log('✗ 该账单不支持余额支付，停止（未扣款）。账单 #' + invoiceId)
  await browser.close()
  process.exit(1)
}

await page.click('.pay-balance button')
const paid = await page
  .waitForFunction(() => location.pathname.startsWith('/services') || !!document.querySelector('.pay-paid'), { timeout: 25000 })
  .then(() => true)
  .catch(() => false)
await sleep(1500)
const balanceAfter = await balance().catch(() => '')
console.log(paid ? '→ 余额支付成功' : '✗ 支付未确认（请人工核对账单 #' + invoiceId + '）')
console.log('   余额:', balanceBefore, '→', balanceAfter || '(页面已跳转)')

// 5) 校验账单状态
if (paid) {
  await page.goto(BASE + '/user/invoices', { waitUntil: 'domcontentloaded', timeout: 20000 })
  await sleep(1500)
  const done = await page.evaluate((id) => {
    const row = Array.from(document.querySelectorAll('.el-table__row')).find((r) => r.textContent.includes(id))
    return row ? /已支付/.test(row.textContent) : null
  }, invoiceId)
  console.log(done === true ? '→ 校验通过：账单 #' + invoiceId + ' 状态为已支付' : '→ 校验：未在账单列表定位到 #' + invoiceId + '（可能已创建服务）')
}

await browser.close()
console.log('\n===== 下单支付 E2E：' + (paid ? '通过' : '未完成') + ' =====')
process.exit(paid ? 0 : 1)
