import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('loadSession', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.restoreAllMocks()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
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

  it('普通并发调用共用一个请求，完成后允许再次刷新', async () => {
    const response = deferred<Response>()
    const fetchMock = vi.fn().mockReturnValueOnce(response.promise)
      .mockResolvedValueOnce(jsonResponse({ csrf: 'csrf-next' }))
    vi.stubGlobal('fetch', fetchMock)
    const mod = await import('./session')
    const first = mod.loadSession()
    const second = mod.loadSession()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    response.resolve(jsonResponse({ csrf: 'csrf-shared' }))
    await Promise.all([first, second])
    expect(mod.useSession().csrf).toBe('csrf-shared')
    await mod.loadSession()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(mod.useSession().csrf).toBe('csrf-next')
  })

  it.each([false, true])('认证后 force 串行刷新正确身份，后台入口=%s', async (adminApp) => {
    const oldResponse = deferred<Response>()
    const freshResponse = deferred<Response>()
    const fetchMock = vi.fn().mockReturnValueOnce(oldResponse.promise)
      .mockReturnValueOnce(freshResponse.promise)
    vi.stubGlobal('fetch', fetchMock)
    const mod = await import('./session')
    mod.setAdminApp(adminApp)
    const state = mod.useSession()
    const before = JSON.stringify(state)
    const oldLoad = mod.loadSession()
    const forcedLoad = mod.loadSession({ force: true })
    const joinedLoad = mod.loadSession()
    expect(fetchMock).toHaveBeenCalledTimes(1)

    oldResponse.resolve(jsonResponse({ csrf: 'csrf-anonymous', site: { name: '旧站点' }, user: null }))
    await oldLoad
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(JSON.stringify(state)).toBe(before)
    // 旧请求 finally 已执行，普通调用仍须加入新的 pending，不能再发请求。
    const laterLoad = mod.loadSession()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const user = { id: 7, isAdmin: false }
    const admin = { id: 9, isAdmin: true }
    freshResponse.resolve(jsonResponse({ csrf: 'csrf-authenticated', user, admin: { path: '/panel', user: admin } }))
    await Promise.all([forcedLoad, joinedLoad, laterLoad])
    expect(state.user).toEqual(user)
    expect(state.adminUser).toEqual(admin)
    expect(mod.currentUser()?.id).toBe(adminApp ? 9 : 7)
    expect(state.csrf).toBe('csrf-authenticated')
    expect(state.loaded).toBe(true)
    expect(state.error).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('旧请求失败不阻断 force，也不写入过时错误', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const oldResponse = deferred<Response>()
    const freshResponse = deferred<Response>()
    const fetchMock = vi.fn().mockReturnValueOnce(oldResponse.promise)
      .mockReturnValueOnce(freshResponse.promise)
    vi.stubGlobal('fetch', fetchMock)
    const mod = await import('./session')
    const oldLoad = mod.loadSession()
    const oldFailure = expect(oldLoad).rejects.toThrow('旧请求失败')
    const forcedResult = mod.loadSession({ force: true }).then(() => '成功', () => '失败')
    oldResponse.reject(new Error('旧请求失败'))
    await oldFailure
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(mod.useSession().error).toBeNull()
    expect(mod.useSession().loaded).toBe(false)
    freshResponse.resolve(jsonResponse({ csrf: 'csrf-new', user: { id: 7, isAdmin: false } }))
    expect(await forcedResult).toBe('成功')
    expect(mod.useSession().user?.id).toBe(7)
    expect(mod.useSession().error).toBeNull()
  })

  it('后一次 force 在前一次请求发出后调用时必须再次串行刷新', async () => {
    const firstResponse = deferred<Response>()
    const secondResponse = deferred<Response>()
    const fetchMock = vi.fn().mockReturnValueOnce(firstResponse.promise)
      .mockReturnValueOnce(secondResponse.promise)
    vi.stubGlobal('fetch', fetchMock)
    const mod = await import('./session')
    const first = mod.loadSession({ force: true })
    const second = mod.loadSession({ force: true })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    firstResponse.resolve(jsonResponse({ user: { id: 1, isAdmin: false } }))
    await first
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(mod.useSession().user).toBeNull()
    secondResponse.resolve(jsonResponse({ user: { id: 2, isAdmin: false } }))
    await second
    expect(mod.useSession().user?.id).toBe(2)
  })

  it('请求失败时保留可诊断的 error 并抛出', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ msg: '会话服务异常' }, 500)))

    const mod = await import('./session')
    await expect(mod.loadSession()).rejects.toThrow('会话服务异常')
    expect(mod.useSession().error).not.toBeNull()
    expect(mod.useSession().loaded).toBe(true)
  })
})
