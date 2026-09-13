/**
 * 快速入口配置
 * 包含：应用列表、快速链接等配置。
 *
 * 使用 LumeIDC 后台的真实路由名，不保留任何 Art 演示应用或外部链接。
 */
import type { FastEnterConfig } from '@/types/config'

const fastEnterConfig: FastEnterConfig = {
  // 显示条件（屏幕宽度）
  minWidth: 1200,
  // 应用列表
  applications: [
    {
      name: '概览看板',
      description: '运营数据与关键指标',
      icon: 'ri:dashboard-line',
      iconColor: '#377dff',
      enabled: true,
      order: 1,
      routeName: 'admin-home'
    },
    {
      name: '订单管理',
      description: '订单查询与处理',
      icon: 'ri:file-list-3-line',
      iconColor: '#ff3b30',
      enabled: true,
      order: 2,
      routeName: 'admin-orders'
    },
    {
      name: '产品管理',
      description: '产品与定价配置',
      icon: 'ri:apps-2-line',
      iconColor: '#7A7FFF',
      enabled: true,
      order: 3,
      routeName: 'admin-products'
    },
    {
      name: '服务实例',
      description: '已开通服务与状态',
      icon: 'ri:server-line',
      iconColor: '#13DEB9',
      enabled: true,
      order: 4,
      routeName: 'admin-services'
    },
    {
      name: '上游服务器',
      description: '服务器与目录导入',
      icon: 'ri:hard-drive-3-line',
      iconColor: '#ffb100',
      enabled: true,
      order: 5,
      routeName: 'admin-servers'
    },
    {
      name: '用户管理',
      description: '用户资料与实名',
      icon: 'ri:user-3-line',
      iconColor: '#ff6b6b',
      enabled: true,
      order: 6,
      routeName: 'admin-users'
    },
    {
      name: '支付网关',
      description: '网关配置与支付',
      icon: 'ri:bank-card-line',
      iconColor: '#38C0FC',
      enabled: true,
      order: 7,
      routeName: 'admin-gateway'
    },
    {
      name: '审计日志',
      description: '后台操作审计记录',
      icon: 'ri:file-text-line',
      iconColor: '#60C041',
      enabled: true,
      order: 8,
      routeName: 'admin-logs'
    }
  ],
  // 快速链接
  quickLinks: [
    {
      name: '退款记录',
      enabled: true,
      order: 1,
      routeName: 'admin-refunds'
    },
    {
      name: '优惠折扣',
      enabled: true,
      order: 2,
      routeName: 'admin-coupons'
    },
    {
      name: '站点设置',
      enabled: true,
      order: 3,
      routeName: 'admin-site'
    },
    {
      name: '通知设置',
      enabled: true,
      order: 4,
      routeName: 'admin-notify-settings'
    },
    {
      name: '安全验证',
      enabled: true,
      order: 5,
      routeName: 'admin-totp'
    },
    {
      name: '系统更新',
      enabled: true,
      order: 6,
      routeName: 'admin-update'
    }
  ]
}

export default Object.freeze(fastEnterConfig)
