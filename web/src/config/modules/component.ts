/**
 * 全局组件配置
 *
 * 统一管理系统级全局组件的注册。
 * 这些组件会在应用启动时按需加载并挂载到后台壳。
 *
 * @module config/component
 */

import { defineAsyncComponent } from 'vue'

/**
 * 全局组件配置列表
 */
export const globalComponentsConfig: GlobalComponentConfig[] = [
  {
    name: '设置面板',
    key: 'settings-panel',
    component: defineAsyncComponent(
      () => import('@/components/core/layouts/art-settings-panel/index.vue')
    ),
    enabled: true
  },
  {
    name: '全局搜索',
    key: 'global-search',
    component: defineAsyncComponent(
      () => import('@/components/core/layouts/art-global-search/index.vue')
    ),
    enabled: true
  }
]

/**
 * 全局组件配置接口
 */
export interface GlobalComponentConfig {
  /** 组件名称 */
  name: string
  /** 组件标识 */
  key: string
  /** 组件 */
  component: any
  /** 是否启用 */
  enabled?: boolean
  /** 组件描述 */
  description?: string
}

/**
 * 获取启用的全局组件
 * @returns 已启用的组件配置列表
 */
export const getEnabledGlobalComponents = () => {
  return globalComponentsConfig.filter((config) => config.enabled !== false)
}

/**
 * 根据 key 获取组件配置
 * @param key 组件标识
 * @returns 组件配置对象
 */
export const getGlobalComponentByKey = (key: string) => {
  return globalComponentsConfig.find((config) => config.key === key)
}
