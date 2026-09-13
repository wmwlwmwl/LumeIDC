/**
 * 前台通用格式化工具。
 *
 * 后端返回的时间为字符串（形如 `2025-01-02 15:04:05` 或 ISO），金额可能是
 * number 或 string。页面统一走这里，避免各页重复 toFixed / 直接打印原始串。
 */

/** 金额：千分位 + 两位小数。非法值返回 '0.00'。 */
export function formatMoney(value: string | number | null | undefined): string {
  const n = typeof value === 'number' ? value : Number(value ?? 0)
  if (!Number.isFinite(n)) return '0.00'
  return n.toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
}

/**
 * 时间：把后端字符串格式化为 `YYYY-MM-DD HH:mm`。
 * 非法/空值原样返回，避免显示 `Invalid Date`。
 */
export function formatDate(value: string | number | Date | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const normalized =
    typeof value === 'string' ? value.replace(/-/g, '/').replace('T', ' ').replace(/\.\d+Z?$/, '') : value
  const d = new Date(normalized)
  if (Number.isNaN(d.getTime())) return String(value)
  const pad = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 允许在描述里渲染的标签（a 单独处理：只保留 http/https 的 href）。 */
const RICH_TEXT_TAGS = new Set([
  'b',
  'strong',
  'em',
  'i',
  'u',
  's',
  'br',
  'p',
  'ul',
  'ol',
  'li',
  'span',
  'small',
  'div',
  'a',
])

/** 外链白名单：只放行 http/https，挡掉 javascript:/data: 等可执行 scheme。 */
const SAFE_URL_RE = /^https?:\/\/[^\s<>"']+$/i

/** 写入属性前的转义（href 来自上游描述，必须转义，防止从属性里逃逸出去）。 */
function escapeAttr(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

/** 解码常见的 HTML 实体（`&lt;ul&gt;` → `<ul>`）。&amp; 必须最后处理。 */
function decodeEntities(input: string): string {
  return input
    .replace(/&#x([0-9a-f]+);/gi, (_, hex) => String.fromCodePoint(parseInt(hex, 16)))
    .replace(/&#(\d+);/g, (_, dec) => String.fromCodePoint(parseInt(dec, 10)))
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&nbsp;/g, ' ')
    .replace(/&amp;/g, '&')
}

/**
 * 富文本安全渲染：先解码 HTML 实体，剔除 script/style，再只放行白名单结构标签并
 * 去掉所有属性，最后把换行转 `<br>`。用于展示上游产品描述里的内嵌 HTML，
 * 返回值可直接用于 v-html。
 */
export function formatRichText(input: string | null | undefined): string {
  if (!input) return ''
  let out = decodeEntities(String(input))
  out = out.replace(/<\s*(script|style)[^>]*>[\s\S]*?<\s*\/\s*\1\s*>/gi, '')
  // 是否原本就带标签：带标签时保留换行作空白（避免在列表项之间插入多余 <br>）
  const hasTags = /<[a-zA-Z/]/.test(out)
  // 已放行的 <a> 层数：href 不合法的链接整对丢掉，别留下孤立的 </a>
  let linkDepth = 0
  out = out.replace(/<[^>]*>/g, (tag) => {
    const match = tag.match(/^<\s*(\/?)\s*([a-zA-Z0-9]+)/)
    if (!match) return ''
    const closing = match[1] === '/'
    const name = match[2].toLowerCase()
    if (name === 'a') {
      // 外链：只保留 http/https 的 href，并统一加固（新窗口 + noopener/nofollow）
      if (closing) {
        if (!linkDepth) return ''
        linkDepth -= 1
        return '</a>'
      }
      const href = tag.match(/\bhref\s*=\s*["']([^"']*)["']/i)?.[1].trim() || ''
      if (!SAFE_URL_RE.test(href)) return ''
      linkDepth += 1
      return `<a href="${escapeAttr(href)}" target="_blank" rel="noopener noreferrer nofollow">`
    }
    if (!RICH_TEXT_TAGS.has(name)) return ''
    return closing ? `</${name}>` : `<${name}>`
  })
  if (!hasTags) out = out.replace(/\r?\n/g, '<br>')
  return out
}
