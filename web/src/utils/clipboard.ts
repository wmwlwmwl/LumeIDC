/**
 * 剪贴板工具。
 *
 * 直接用 `navigator.clipboard?.writeText(x).then(...)` 有个隐蔽的坑：可选链会短路
 * **整条链**，所以当 `navigator.clipboard` 为 undefined 时，`.then/.catch` 都不会执行，
 * 结果是既没复制、也不报错——用户点「复制」什么都不发生。
 * 而 Clipboard API 只在安全上下文（HTTPS / localhost）下存在，纯 HTTP 访问（如
 * http://<IP>:8080）时它就是 undefined，必须降级。
 *
 * 统一走 copyText()：优先 Clipboard API，不可用/被拒时降级到临时 textarea。
 */

/** 降级方案：临时 textarea + execCommand('copy')，兼容非安全上下文。 */
function fallbackCopy(text: string): boolean {
  const ta = document.createElement('textarea')
  ta.value = text
  // readonly 避免移动端弹键盘；移出视口避免页面跳动
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.top = '-9999px'
  ta.style.opacity = '0'
  document.body.appendChild(ta)

  const selection = document.getSelection()
  const previous = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null

  ta.select()
  ta.setSelectionRange(0, ta.value.length)

  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }

  document.body.removeChild(ta)
  if (previous && selection) {
    selection.removeAllRanges()
    selection.addRange(previous)
  }
  return ok
}

/**
 * 复制文本到剪贴板。返回是否成功（空文本视为失败）。
 * 调用方自行决定提示文案，例如：
 * `if (await copyText(v)) ElMessage.success('已复制')`
 */
export async function copyText(text: string | null | undefined): Promise<boolean> {
  const value = text ?? ''
  if (!value) return false
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // 权限被拒等：继续走降级
    }
  }
  return fallbackCopy(value)
}
