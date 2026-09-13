import { describe, it, expect, vi } from 'vitest'

vi.mock('element-plus', () => ({ ElMessage: { error: vi.fn(), success: vi.fn() } }))

import { isExpiredSessionResponse, buildQuery } from './index'

describe('isExpiredSessionResponse', () => {
  it('识别后端会话/CSRF 失效消息', () => {
    expect(isExpiredSessionResponse(401, '/api/order', { ok: 0, msg: '会话已过期，请重新登录' })).toBe(true)
    expect(isExpiredSessionResponse(401, '/api/order', { ok: 0, msg: '页面已过期，请重新登录后重试' })).toBe(true)
    expect(isExpiredSessionResponse(401, '/api/order', { ok: 0, msg: '登录已过期' })).toBe(true)
  })

  it('不把业务 401（密码/验证码错误）当作会话失效', () => {
    expect(isExpiredSessionResponse(401, '/login', { ok: 0, msg: '密码错误' })).toBe(false)
    expect(isExpiredSessionResponse(401, '/login', { ok: 0, msg: '验证码错误' })).toBe(false)
    expect(isExpiredSessionResponse(400, '/api/order', { ok: 0, msg: '会话已过期' })).toBe(false)
  })

  it('logout 不回退重试', () => {
    expect(isExpiredSessionResponse(401, '/logout', { ok: 0, msg: '页面已过期' })).toBe(false)
  })
})

describe('buildQuery', () => {
  it('跳过空值并展开数组', () => {
    expect(buildQuery({ a: 1, b: '', c: null, d: ['x', 'y'] })).toBe('?a=1&d=x&d=y')
    expect(buildQuery()).toBe('')
  })
})
