import { describe, expect, it } from 'vitest'
import { adminMenu, mergePluginMenus, type PluginMenuInfo } from './menu'

const plugin = (over: Partial<PluginMenuInfo>): PluginMenuInfo => ({
  name: 'demo',
  title: '演示插件',
  enabled: true,
  hasAdminPage: true,
  ...over,
})

describe('mergePluginMenus', () => {
  it('声明业务分组的插件挂进对应分组末尾', () => {
    const merged = mergePluginMenus(adminMenu, [plugin({ name: 'tickets', menuTitle: '工单管理', menuParent: 'users' })])
    const users = merged.find((g) => g.name === 'group-users')!
    expect(users.children!.at(-1)).toMatchObject({ path: '/plugin/tickets', meta: { title: '工单管理' } })
    // 其他分组不受影响，且不产生「插件」组
    expect(merged.find((g) => g.name === 'group-plugins')).toBeUndefined()
    expect(merged.length).toBe(adminMenu.length)
  })

  it('未声明/未知分组的插件收拢进「插件」分组', () => {
    const merged = mergePluginMenus(adminMenu, [
      plugin({ name: 'webhooknotify' }),
      plugin({ name: 'x', menuParent: 'not-exists' }),
    ])
    const group = merged.find((g) => g.name === 'group-plugins')!
    expect(group.children!.map((c) => c.path)).toEqual(['/plugin/webhooknotify', '/plugin/x'])
  })

  it('禁用或无后台页的插件不进菜单', () => {
    const merged = mergePluginMenus(adminMenu, [
      plugin({ name: 'off', enabled: false }),
      plugin({ name: 'nopage', hasAdminPage: false }),
    ])
    expect(merged).toBe(adminMenu) // 无有效插件时原样返回
  })

  it('不污染原 adminMenu（不可变合并）', () => {
    const before = adminMenu.find((g) => g.name === 'group-users')!.children!.length
    mergePluginMenus(adminMenu, [plugin({ menuParent: 'users' })])
    expect(adminMenu.find((g) => g.name === 'group-users')!.children!.length).toBe(before)
    expect(adminMenu.find((g) => g.name === 'group-plugins')).toBeUndefined()
  })
})
