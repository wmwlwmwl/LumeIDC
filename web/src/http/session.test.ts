import { describe, it, expect, vi, beforeEach } from 'vitest'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('loadSession', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.restoreAllMocks()
  })

  it('匿名会话写入 CSRF、站点信息并标记已加载', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        csrf: 'csrf-abc',
        site: { name: 'Lume 云' },
        user: null,
        admin: { path: '/panel' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const mod = await import('./session')
    await mod.loadSession()

    const s = mod.useSession()
    expect(s.csrf).toBe('csrf-abc')
    expect(s.user).toBeNull()
    expect(s.loaded).toBe(true)
    expect(s.site.name).toBe('Lume 云')
    expect(s.adminPath).toBe('/panel')
    // 请求必须走会话端点，且带 no-store 防止缓存旧 CSRF。
    const [, init] = fetchMock.mock.calls[0]
    expect(String(fetchMock.mock.calls[0][0])).toContain('/session')
    expect(init.cache).toBe('no-store')
  })

  it('服务重启后旧 Cookie 被新的匿名会话覆盖', async () => {
    const mod = await import('./session')
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          csrf: 'csrf-1',
          site: {},
          user: { id: 7, isAdmin: true },
          admin: { path: '/admin' },
        }),
      ),
    )
    await mod.loadSession()
    expect(mod.useSession().user?.id).toBe(7)

    // 模拟 Go 重启：旧 Cookie 已无对应内存会话，返回新匿名会话。
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ csrf: 'csrf-2', site: {}, user: null })),
    )
    await mod.loadSession({ force: true })
    expect(mod.useSession().user).toBeNull()
    expect(mod.useSession().csrf).toBe('csrf-2')
  })

  it('请求失败时保留可诊断的 error 并抛出', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ msg: 'boom' }, 500)))

    const mod = await import('./session')
    await expect(mod.loadSession()).rejects.toThrow()
    expect(mod.useSession().error).not.toBeNull()
    expect(mod.useSession().loaded).toBe(true)
  })
})
