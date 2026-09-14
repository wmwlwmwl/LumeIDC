/**
 * 图标子集生成：扫描 src 下所有 `ri:xxx` / `vaadin:xxx` 字面量引用，
 * 从 @iconify-json/<集合> 提取子集，避免全量图标集打进入口包。
 *
 * 产物：src/utils/ui/icons-subset.json（IconifyJSON 数组，构建生成物，已 gitignore），
 * 由 utils/ui/iconify-loader.ts 逐集合注册到本地。
 * 新增其他图标集时在 ICON_SETS 里加前缀并安装对应 @iconify-json/<集合> 即可。
 * 用法：node scripts/build-icons.mjs（build/dev 前自动执行）。
 */
import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const srcDir = join(root, 'src')
const outFile = join(srcDir, 'utils', 'ui', 'icons-subset.json')

// 支持的图标集前缀（需安装 @iconify-json/<前缀>）
const ICON_SETS = ['ri', 'vaadin']

// 递归收集源码文件
function walk(dir, files = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) walk(p, files)
    else if (/\.(vue|ts|js|tsx|jsx)$/.test(name)) files.push(p)
  }
  return files
}

// 动态拼接检测：`ri:xxx${...}` 或 `ri:xxx` + 字符串拼接，静态提取无法确定图标名。
const DYNAMIC_RE = /\b(?:ri|vaadin):[^\s'"`,;)]*(?:\$\{|\+)/
// 字面量引用：集合前缀 + 图标名（kebab-case）
const USE_RE = /\b(ri|vaadin):([a-z0-9]+(?:-[a-z0-9]+)*)\b/g

const files = walk(srcDir)
const used = new Map(ICON_SETS.map((s) => [s, new Set()]))
const dynamicHits = []
for (const f of files) {
  const text = readFileSync(f, 'utf8')
  for (const m of text.matchAll(USE_RE)) used.get(m[1]).add(m[2])
  if (DYNAMIC_RE.test(text)) dynamicHits.push(f)
}
if (dynamicHits.length) {
  console.error(`[build-icons] 检测到动态拼接的图标名（无法静态提取），请改为字面量：`)
  for (const f of dynamicHits) console.error(`  ${f}`)
  process.exit(1)
}

const totalUsed = [...used.values()].reduce((n, s) => n + s.size, 0)
if (!totalUsed) {
  console.error('[build-icons] 未扫描到任何图标引用')
  process.exit(1)
}

const subset = []
const missing = []
for (const set of ICON_SETS) {
  const names = used.get(set)
  if (!names.size) continue
  const full = JSON.parse(readFileSync(join(root, 'node_modules', '@iconify-json', set, 'icons.json'), 'utf8'))

  // 解析 aliases：引用别名时需带上其 parent 图标
  const picked = new Set()
  for (const name of names) {
    if (full.icons[name]) {
      picked.add(name)
    } else if (full.aliases?.[name]) {
      picked.add(full.aliases[name].parent)
    } else {
      missing.push(`${set}:${name}`)
    }
  }
  subset.push({
    prefix: full.prefix,
    width: full.width,
    height: full.height,
    icons: Object.fromEntries([...picked].sort().map((n) => [n, full.icons[n]])),
  })
}
if (missing.length) {
  console.error(`[build-icons] 以下图标在对应 @iconify-json 集合中不存在：${missing.join(', ')}`)
  process.exit(1)
}

// aliases 内部引用：parent 已含于 icons，若 alias 集合里有被引用的别名则保留映射
writeFileSync(outFile, JSON.stringify(subset))

console.log(`[build-icons] 扫描 ${files.length} 个文件，提取 ${totalUsed} 个图标（${subset.map((s) => `${s.prefix}:${Object.keys(s.icons).length}`).join(' ')}）→ ${outFile}`)
