import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { copyText } from './clipboard'

// copyText 的两条路径：Clipboard API（HTTPS / localhost 等安全上下文）与 textarea 降级
// （纯 HTTP 访问时 navigator.clipboard 不存在）。
// 回归重点：clipboard 为 undefined 时必须真的降级，而不是像
// `navigator.clipboard?.writeText(x).then(...)` 那样被可选链短路成"什么都不做"。

function stubClipboard(writeText?: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', {
    value: writeText ? { writeText } : undefined,
    configurable: true,
    writable: true,
  })
}

let execCommand: ReturnType<typeof vi.fn>

beforeEach(() => {
  execCommand = vi.fn(() => true)
  Object.defineProperty(document, 'execCommand', {
    value: execCommand,
    configurable: true,
    writable: true,
  })
})

afterEach(() => {
  delete (navigator as { clipboard?: unknown }).clipboard
  vi.restoreAllMocks()
})

describe('copyText', () => {
  it('安全上下文：走 Clipboard API，不触发降级', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    stubClipboard(writeText)

    await expect(copyText('abc')).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledWith('abc')
    expect(execCommand).not.toHaveBeenCalled()
  })

  it('非安全上下文（navigator.clipboard 不存在）：降级到 execCommand 并返回成功', async () => {
    stubClipboard(undefined)

    await expect(copyText('abc')).resolves.toBe(true)
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('Clipboard API 抛错（如权限被拒）时同样降级', async () => {
    stubClipboard(() => Promise.reject(new Error('NotAllowedError')))

    await expect(copyText('abc')).resolves.toBe(true)
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('两条路径都失败时返回 false，供调用方提示「复制失败」', async () => {
    stubClipboard(undefined)
    execCommand.mockReturnValue(false)

    await expect(copyText('abc')).resolves.toBe(false)
  })

  it('空值直接返回 false，不触碰任何复制通道', async () => {
    stubClipboard(vi.fn(() => Promise.resolve()))

    await expect(copyText('')).resolves.toBe(false)
    await expect(copyText(undefined)).resolves.toBe(false)
    await expect(copyText(null)).resolves.toBe(false)
    expect(execCommand).not.toHaveBeenCalled()
  })

  it('降级用的临时 textarea 会被清理干净', async () => {
    stubClipboard(undefined)

    await copyText('abc')
    expect(document.querySelectorAll('textarea')).toHaveLength(0)
  })
})
