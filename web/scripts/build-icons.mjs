/**
 * 图标子集生成：扫描 src 下所有 `ri:xxx` 字面量引用，从 @iconify-json/ri 提取子集，
 * 避免全量图标集（~1MB）打进入口包。
 *
 * 产物：src/utils/ui/icons-subset.json（构建生成物，已 gitignore）。
 * 用法：node scripts/build-icons.mjs（build/dev 前自动执行）。
 */
import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const srcDir = join(root, 'src')
const outFile = join(srcDir, 'utils', 'ui', 'icons-subset.json')

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
const DYNAMIC_RE = /\bri:[^\s'"`,;)]*(?:\$\{|\+)/
// 字面量引用：ri: 图标名（kebab-case）
const ICON_RE = /\bri:([a-z0-9]+(?:-[a-z0-9]+)*)\b/g

const files = walk(srcDir)
const used = new Set()
const dynamicHits = []
for (const f of files) {
  const text = readFileSync(f, 'utf8')
  for (const m of text.matchAll(ICON_RE)) used.add(m[1])
  if (DYNAMIC_RE.test(text)) dynamicHits.push(f)
}
if (dynamicHits.length) {
  console.error(`[build-icons] 检测到动态拼接的图标名（无法静态提取），请改为字面量：`)
  for (const f of dynamicHits) console.error(`  ${f}`)
  process.exit(1)
}

if (!used.size) {
  console.error('[build-icons] 未扫描到任何 ri: 图标引用')
  process.exit(1)
}

const full = JSON.parse(readFileSync(join(root, 'node_modules', '@iconify-json', 'ri', 'icons.json'), 'utf8'))

// 解析 aliases：引用别名时需带上其 parent 图标
const missing = []
const names = new Set()
for (const name of used) {
  if (full.icons[name]) {
    names.add(name)
  } else if (full.aliases?.[name]) {
    names.add(full.aliases[name].parent)
  } else {
    missing.push(name)
  }
}
if (missing.length) {
  console.error(`[build-icons] 以下图标在 @iconify-json/ri 中不存在：${missing.join(', ')}`)
  process.exit(1)
}

const subset = {
  prefix: full.prefix,
  width: full.width,
  height: full.height,
  icons: Object.fromEntries([...names].sort().map((n) => [n, full.icons[n]])),
}
// aliases 内部引用：parent 已含于 icons，若 alias 集合里有被引用的别名则保留映射
writeFileSync(outFile, JSON.stringify(subset))

console.log(`[build-icons] 扫描 ${files.length} 个文件，提取 ${names.size}/${Object.keys(full.icons).length} 个图标 → ${outFile}`)
