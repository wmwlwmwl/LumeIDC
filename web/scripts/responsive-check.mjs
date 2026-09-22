/**
 * 响应式 / 布局回归检查（开发辅助脚本，不参与构建）
 *
 * 用本机 Chrome 以无头模式打开 Vite 开发服务器，拦截 /session 与 /__api/*
 * 返回模拟数据，然后在多档视口下检测：
 *   1. 文档是否出现横向溢出（scrollWidth > clientWidth）
 *   2. 哪些元素超出视口左右边界（定位像素级溢出源）
 *   3. 控制台错误
 *
 * 用法：先 `npm run dev`，再 `node scripts/responsive-check.mjs`
 */
import fs from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.CHECK_BASE || 'http://localhost:5173'
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

const SESSION = {
  csrf: 'check-token',
  site: { name: 'LumeIDC', description: '', keywords: '', email: '', phone: '', hours: '' },
  user: { id: 1, isAdmin: true, email: 'admin@example.com', name: '管理员', balance: '100.00' },
  // 后台入口按管理员通道判登录态（与前台用户通道彼此独立）
  admin: { path: '/admin', user: { id: 1, isAdmin: true } },
  auth: {},
}

const GENERIC = {
  ok: 1,
  list: [],
  total: 0,
  profit: '0.00',
  users: 0,
  orders: 0,
  services: 0,
  products: [],
  types: [],
  rows: [],
  servers: [],
  providers: [],
  banners: [],
  catalog: [],
  announcements: [],
  notices: [],
  coupons: [],
  refunds: [],
  logs: [],
  gateways: [],
  verifications: [],
  fid: '',
  gid: '',
  credential_fields: {},
  values: {},
  user: { id: 1, email: 'admin@example.com', name: '管理员', balance: '100.00', status: 1, email_status: '已验证', phone_status: '未绑定', registered_at: '-', last_login_at: '-', phone_masked: '' },
  stats: { service_count: 0, active_count: 0, unpaid_count: 0, paid_total: '0.00' },
  realname: { status: '-', name: '', number: '', submitted_at: '', reviewed_at: '', front_url: '', back_url: '' },
  balance_logs: [],
  admin_logs: [],
  current_version: '2.3.0',
  pending_restart: false,
  disabled: true,
  enabled: false,
  secret: 'JBSWY3DPEHPK3PXP',
  uri: 'otpauth://totp/LumeIDC',
  status: 'ok',
}

const SERVICE_DETAIL = {
  ok: 1,
  csrf: 'check-token',
  svc: {
    id: 1,
    name: '香港弹性云 1核 1G 1M带宽不限流量 xxx元',
    status: 1,
    status_text: '激活',
    hostname: 'hklume1',
    product_id: 1,
    remark: '',
    expires_at: '2026-10-11 23:40',
    created_at: '2026-09-11 23:40',
    cycle: '月付',
    amount: '7.00',
    configs: [{ name: '流量', value: '1g', price: 2 }],
    provider: 'zjmf',
  },
  host: {
    username: 'root',
    status: '运行中',
    os: 'linux',
    ip: '103.1.2.3',
    additional_ips: ['103.1.2.4'],
    bw_limit: '1M',
    bw_usage: '0G',
    datacenter: '香港',
    os_version: 'CentOS 7.9',
    port: '22',
  },
  invoices: [],
}

const TARGETS = [
  ['public-home', '/'],
  ['public-catalog', '/cart'],
  ['public-login', '/login'],
  ['public-register', '/register'],
  ['service-detail', '/services/1'],
  ['admin-home', '/admin#/'],
  ['admin-users', '/admin#/users'],
  ['admin-user-edit', '/admin#/users/1/edit'],
  ['admin-orders', '/admin#/orders'],
  ['admin-products', '/admin#/products'],
  ['admin-product-form', '/admin#/products/new'],
  ['admin-services', '/admin#/services'],
  ['admin-servers', '/admin#/servers'],
  ['admin-server-form', '/admin#/servers/new'],
  ['admin-coupons', '/admin#/coupons'],
  ['admin-refunds', '/admin#/refunds'],
  ['admin-logs', '/admin#/logs'],
  ['admin-gateway', '/admin#/gateway'],
  ['admin-site', '/admin#/site'],
  ['admin-settings', '/admin#/settings'],
  ['admin-totp', '/admin#/totp'],
  ['admin-password', '/admin#/password'],
  ['admin-update', '/admin#/update'],
  ['admin-types', '/admin#/types'],
  ['admin-announcements', '/admin#/announcements'],
  ['admin-verifications', '/admin#/verifications'],
  ['admin-plugin-refund', '/admin#/plugin/refund'],
  ['client-plugin-refund', '/plugin/refund'],
]

const VIEWPORTS = [
  ['desktop', 1440, 900],
  ['laptop', 1024, 768],
  ['tablet', 768, 1024],
  ['phone', 390, 844],
]

const browser = await puppeteer.launch({
  executablePath,
  headless: 'new',
  args: ['--no-sandbox', '--disable-gpu', '--hide-scrollbars'],
})

const results = []
// 调试用：CHECK_TARGET=service-detail CHECK_VP=phone 可只跑单项（名字子串匹配）
const ONLY_TARGET = process.env.CHECK_TARGET || ''
const ONLY_VP = process.env.CHECK_VP || ''
for (const [vpName, width, height] of VIEWPORTS) {
  if (ONLY_VP && !vpName.includes(ONLY_VP)) continue
  const page = await browser.newPage()
  await page.setViewport({ width, height, isMobile: vpName === 'phone' })
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const url = req.url()
    if (url.includes('/__api/session') || url.endsWith('/session')) {
      req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(SESSION) })
      return
    }
    // 服务详情：只有详情接口给真实载荷，其余子接口走 GENERIC，避免面板因缺数据不渲染
    if (new URL(url).pathname === '/__api/services/1') {
      req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(SERVICE_DETAIL) })
      return
    }
    if (url.includes('/__api/')) {
      req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(GENERIC) })
      return
    }
    if (!url.startsWith(BASE) && !url.startsWith('data:')) {
      // 外网资源（字体/图标）直接短路，保证离线可测
      req.respond({ status: 200, contentType: 'text/plain', body: '' }).catch(() => {})
      return
    }
    req.continue()
  })

  const consoleErrors = []
  page.on('console', (msg) => {
    if (msg.type() === 'error' && !msg.text().includes('Failed to load resource')) {
      consoleErrors.push(msg.text())
    }
  })
  page.on('pageerror', (err) => consoleErrors.push('pageerror: ' + err.message))

  for (const [name, path] of TARGETS) {
    if (ONLY_TARGET && !name.includes(ONLY_TARGET)) continue
    try {
      await page.goto(BASE + path, { waitUntil: 'domcontentloaded', timeout: 20000 })
      await new Promise((r) => setTimeout(r, 700))
      const report = await page.evaluate(() => {
        const doc = document.documentElement
        const vw = window.innerWidth
        const overflow = doc.scrollWidth - doc.clientWidth

        const desc = (el) => {
          const cls = typeof el.className === 'string' ? el.className.trim().slice(0, 50) : ''
          return `<${el.tagName.toLowerCase()}${cls ? ` class="${cls}"` : ''}>`
        }
        const chain = (el) => {
          const out = []
          let p = el.parentElement
          while (p && p !== doc && out.length < 4) {
            out.push(desc(p))
            p = p.parentElement
          }
          return out.join(' < ')
        }

        // 元素若位于可裁剪容器（overflow-x 非 visible）内，其超出不算页面级溢出。
        // 到 body 为止：body 有全局 overflow-x:hidden，若把它算进来则所有元素都“被裁剪”，
        // 溢出源将永远报不出来（这正是本检测项此前形同虚设的原因）。
        const inClippingAncestor = (el) => {
          let p = el.parentElement
          while (p && p !== document.body) {
            const ox = getComputedStyle(p).overflowX
            if (ox === 'hidden' || ox === 'auto' || ox === 'scroll' || ox === 'clip') return true
            p = p.parentElement
          }
          return false
        }

        const offenders = []
        // 比视口还宽的元素：横向溢出的直接来源，不受裁剪判定影响
        const wide = []
        for (const el of Array.from(document.querySelectorAll('body *'))) {
          const r = el.getBoundingClientRect()
          if (r.width === 0 || r.height === 0) continue
          // 已被裁剪容器裹住的宽内容（如 el-table 横向滚动区）不会撑宽页面，不算问题
          if (r.width > vw + 1 && !inClippingAncestor(el)) {
            wide.push({ el: desc(el), w: Math.round(r.width), chain: chain(el) })
          }
          if (r.right > vw + 1 || r.left < -1) {
            if (inClippingAncestor(el)) continue
            offenders.push({
              tag: el.tagName.toLowerCase(),
              cls: (typeof el.className === 'string' ? el.className : '').slice(0, 60),
              left: Math.round(r.left),
              right: Math.round(r.right),
            })
            if (offenders.length >= 6) break
          }
        }
        wide.sort((a, b) => b.w - a.w)

        // 内容溢出但 overflow-x 为 visible 的元素：文本/内联内容漏出，元素盒子本身却不宽。
        // clientWidth 为 0 的盒子（折叠菜单等）scrollWidth 无意义，直接跳过。
        const leaky = []
        for (const el of Array.from(document.querySelectorAll('body *'))) {
          if (getComputedStyle(el).overflowX !== 'visible') continue
          if (el.clientWidth === 0 || inClippingAncestor(el)) continue
          const diff = el.scrollWidth - el.clientWidth
          if (diff <= 1) continue
          // JS 定位的浮层（popper/tooltip/消息通知）内部几像素溢出由浮层自身定位决定，不算页面布局溢出
          if (el.closest('.el-popper, .el-message, .el-notification')) continue
          leaky.push({ el: desc(el), diff, chain: chain(el), node: el })
        }
        leaky.sort((a, b) => b.diff - a.diff)

        // 最严重的漏出元素：附带其自身样式与后 3 层后代实测尺寸，用于定位不肯收缩的那个
        let worst = null
        if (leaky.length) {
          const el = leaky[0].node
          const cs = getComputedStyle(el)
          const info = (node, depth) => {
            const c = getComputedStyle(node)
            const r = node.getBoundingClientRect()
            return {
              el: desc(node),
              depth,
              w: Math.round(r.width),
              left: Math.round(r.left),
              right: Math.round(r.right),
              display: c.display,
              flexDir: c.flexDirection,
              alignItems: c.alignItems,
              flexWrap: c.flexWrap,
              minW: c.minWidth,
              flex: c.flex,
              ws: c.whiteSpace,
              ovx: c.overflowX,
            }
          }
          const walk = (node, depth, out) => {
            if (depth > 3) return
            for (const ch of Array.from(node.children)) {
              out.push(info(ch, depth))
              walk(ch, depth + 1, out)
            }
          }
          const descendants = []
          walk(el, 1, descendants)
          worst = {
            self: leaky[0].el,
            box: { client: el.clientWidth, scroll: el.scrollWidth },
            style: {
              display: cs.display,
              flexDir: cs.flexDirection,
              alignItems: cs.alignItems,
              flexWrap: cs.flexWrap,
              gap: cs.gap,
              padding: cs.padding,
              overflowX: cs.overflowX,
            },
            descendants: descendants.slice(0, 12),
          }
        }

        return {
          overflow,
          offenders,
          wide: wide.slice(0, 6),
          leaky: leaky.slice(0, 8).map(({ el, diff, chain: ch }) => ({ el, diff, chain: ch })),
          worst,
        }
      })
      const shell = await page.evaluate(() => ({
        hasLayout: !!document.querySelector('.app-layout') || !!document.querySelector('.public-shell'),
        textLen: (document.body.innerText || '').trim().length,
      }))
      results.push({ vpName, name, path, ...report, ...shell, errors: consoleErrors.slice(0, 3) })
      consoleErrors.length = 0
    } catch (err) {
      results.push({ vpName, name, path, error: String(err).slice(0, 120) })
    }
  }
  await page.close()
}

await browser.close()

const problems = results.filter(
  (r) =>
    r.error ||
    r.overflow > 1 ||
    (r.wide && r.wide.length) ||
    (r.leaky && r.leaky.length) ||
    (r.offenders && r.offenders.length) ||
    (r.errors && r.errors.length) ||
    r.hasLayout === false ||
    (typeof r.textLen === 'number' && r.textLen < 20),
)
console.log(`\n===== 检查 ${results.length} 项，异常 ${problems.length} 项 =====`)
for (const r of problems) {
  console.log(`\n[${r.vpName}] ${r.name} (${r.path})`)
  if (r.error) console.log('  load-error:', r.error)
  if (r.hasLayout === false) console.log('  未渲染布局壳 (.app-layout/.public-shell 缺失)')
  if (typeof r.textLen === 'number' && r.textLen < 20) console.log('  页面文本过少，疑似空白:', r.textLen)
  if (r.overflow > 1) console.log('  横向溢出:', r.overflow + 'px')
  if (r.wide?.length) r.wide.forEach((w) => console.log(`    超宽 ${w.w}px ${w.el}  ← ${w.chain}`))
  if (r.leaky?.length) r.leaky.forEach((l) => console.log(`    内容漏出 +${l.diff}px ${l.el}  ← ${l.chain}`))
  if (r.worst) {
    console.log(`    最宽漏出元素 ${r.worst.self}  client=${r.worst.box.client} scroll=${r.worst.box.scroll}`)
    console.log(`      自身样式 ${JSON.stringify(r.worst.style)}`)
    r.worst.descendants.forEach((c) =>
      console.log(
        `      ${'  '.repeat(c.depth)}${c.el} w=${c.w} ${c.left}..${c.right} display=${c.display}/${c.flexDir}/${c.alignItems}/${c.flexWrap} min-width=${c.minW} flex=${c.flex} ws=${c.ws} ovx=${c.ovx}`,
      ),
    )
  }
  if (r.offenders?.length) r.offenders.forEach((o) => console.log(`    overflow <${o.tag} class="${o.cls}"> ${o.left}..${o.right}`))
  if (r.errors?.length) r.errors.forEach((e) => console.log('    console:', e))
}
