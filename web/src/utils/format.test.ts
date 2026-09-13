import { describe, it, expect } from 'vitest'
import { formatRichText } from './format'

// 上游描述里的外链：只放行 http/https，并统一加固；其它 scheme 连标签一起丢掉。
describe('formatRichText 外链', () => {
  it('保留 http/https 链接并加上 target/rel', () => {
    const out = formatRichText(
      '<a href="https://nodequality.com/r/abc" target="_blank"><strong>线路测试报告</strong></a>',
    )
    expect(out).toBe(
      '<a href="https://nodequality.com/r/abc" target="_blank" rel="noopener noreferrer nofollow"><strong>线路测试报告</strong></a>',
    )
  })

  it('javascript: / data: / 相对地址一律丢掉标签，只留文字', () => {
    expect(formatRichText('<a href="javascript:alert(1)">点我</a>')).toBe('点我')
    expect(formatRichText('<a href="data:text/html;base64,PA==">点我</a>')).toBe('点我')
    expect(formatRichText('<a href="/user/verification">点我</a>')).toBe('点我')
  })

  it('href 里带引号/尖括号的内容不会逃逸出属性', () => {
    const out = formatRichText('<a href=\'https://a.com/?x="><img src=x onerror=1>\'>x</a>')
    expect(out).not.toContain('<img')
    expect(out).not.toContain('onerror')
    expect(out).toContain('x')
  })

  it('没有 href 的 <a> 只留文字', () => {
    expect(formatRichText('<a>纯文字</a>')).toBe('纯文字')
  })
})
