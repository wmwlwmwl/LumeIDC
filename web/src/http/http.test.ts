import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

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

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status })
}

describe('HTTP 会话恢复', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it.each([false, true])('并发401共享会话请求并用新CSRF各重试一次，已有焦点请求=%s', async (hasFocusRequest) => {
    let resolveSession!: (response: Response) => void
    const response = new Promise<Response>((resolve) => { resolveSession = resolve })
    const sessionFetch = vi.fn().mockReturnValue(response)
    const apiFetch = vi.fn((_url: string, init: RequestInit) => {
      const csrf = (init.headers as Record<string, string>)['X-CSRF-Token']
      return Promise.resolve(csrf === 'csrf-new'
        ? jsonResponse({ ok: 1 })
        : jsonResponse({ ok: 0, msg: '会话已过期' }, 401))
    })
    vi.stubGlobal('fetch', vi.fn((url: string, init: RequestInit) => (
      url.endsWith('/session') ? sessionFetch() : apiFetch(url, init)
    )))
    const session = await import('./session')
    const loadSpy = vi.spyOn(session, 'loadSession')
    const { http, setOnUnauthorized } = await import('./index')
    const unauthorized = vi.fn()
    setOnUnauthorized(unauthorized)
    session.useSession().csrf = 'csrf-old'
    const focusLoad = hasFocusRequest ? session.loadSession() : undefined
    const requests = [http.post('/api/first'), http.post('/api/second')]
    await vi.waitFor(() => expect(loadSpy).toHaveBeenCalledTimes(hasFocusRequest ? 3 : 2))
    expect(sessionFetch).toHaveBeenCalledTimes(1)
    expect(apiFetch).toHaveBeenCalledTimes(2)
    resolveSession(jsonResponse({ csrf: 'csrf-new' }))
    await expect(Promise.all(requests)).resolves.toEqual([{ ok: 1 }, { ok: 1 }])
    await focusLoad
    expect(sessionFetch).toHaveBeenCalledTimes(1)
    expect(apiFetch).toHaveBeenCalledTimes(4)
    expect(unauthorized).not.toHaveBeenCalled()
    for (const [, init] of apiFetch.mock.calls.slice(2)) {
      expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-new')
    }
  })

  it('恢复失败保留原始401，恢复后仍401最多重试一次', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const sessionFetch = vi.fn()
      .mockRejectedValueOnce(new Error('会话恢复失败'))
      .mockImplementation(() => Promise.resolve(jsonResponse({ csrf: 'csrf-new' })))
    const apiFetch = vi.fn(() => Promise.resolve(jsonResponse({ ok: 0, msg: '会话已过期' }, 401)))
    vi.stubGlobal('fetch', vi.fn((url: string) => url.endsWith('/session') ? sessionFetch() : apiFetch()))
    const { http, setOnUnauthorized } = await import('./index')
    const unauthorized = vi.fn()
    setOnUnauthorized(unauthorized)
    await expect(http.post('/api/first')).rejects.toMatchObject({ status: 401, message: '会话已过期' })
    expect(apiFetch).toHaveBeenCalledTimes(1)
    expect(unauthorized).toHaveBeenCalledTimes(1)
    await expect(http.post('/api/second')).rejects.toMatchObject({ status: 401, message: '会话已过期' })
    expect(sessionFetch).toHaveBeenCalledTimes(2)
    expect(apiFetch).toHaveBeenCalledTimes(3)
  })
})
