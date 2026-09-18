<script setup lang="ts">
import { ref, computed, type Component } from 'vue'
import { useRoute } from 'vue-router'
import { Grid, Monitor, Document, Wallet, Bell, User, Lock, ArrowRight, ChatDotRound, Ticket } from '@element-plus/icons-vue'
import { useSession } from '../http/session'
import { formatMoney } from '@/utils/format'
import PublicContainer from '@/components/public/PublicContainer.vue'

interface MenuLink {
  to: string
  label: string
  paths: string[]
  icon: Component
  /** 精确匹配当前路径（用于 /user 概览，避免匹配到所有 /user/* 子页） */
  exact?: boolean
}

const session = useSession()
const route = useRoute()
const menuOpen = ref(false)
const balance = computed(() => formatMoney(session.user?.balance ?? 0))

function isActive(link: MenuLink): boolean {
  return link.paths.some((p) =>
    link.exact ? route.path === p : route.path === p || route.path.startsWith(p + '/'),
  )
}

const accountLinks: MenuLink[] = [
  { to: '/user', label: '概览', paths: ['/user'], icon: Grid, exact: true },
  { to: '/services', label: '我的服务', paths: ['/services'], icon: Monitor },
  { to: '/user/invoices', label: '财务记录', paths: ['/user/invoices'], icon: Document },
  { to: '/user/recharge', label: '账户充值', paths: ['/user/recharge'], icon: Wallet },
  { to: '/user/promotion-coupons', label: '活动优惠券', paths: ['/user/promotion-coupons'], icon: Ticket },
  { to: '/notifications', label: '消息中心', paths: ['/notifications'], icon: Bell },
  { to: '/tickets', label: '工单支持', paths: ['/tickets'], icon: ChatDotRound },
]
const settingLinks: MenuLink[] = [
  { to: '/user/verification', label: '实名认证', paths: ['/user/verification'], icon: User },
  { to: '/user/profile', label: '资料与手机号', paths: ['/user/profile'], icon: User },
  { to: '/user/password', label: '安全设置', paths: ['/user/password'], icon: Lock },
]
</script>

<template>
  <PublicContainer>
    <button
      type="button"
      class="account-mobile-bar"
      :aria-expanded="menuOpen"
      aria-controls="account-sidebar"
      @click="menuOpen = !menuOpen"
    >
      <span class="account-mobile-bar__copy">
        <strong>账户中心</strong><small>管理你的服务与账单</small>
      </span>
      <span class="account-mobile-bar__balance">
        ￥{{ balance }} <span>{{ menuOpen ? '收起' : '菜单' }}</span>
      </span>
    </button>

    <div class="account-layout">
      <aside id="account-sidebar" class="account-sidebar" :class="{ 'is-open': menuOpen }">
        <div class="art-card account-panel">
          <div class="account-profile">
            <div class="account-profile__avatar"><el-icon><User /></el-icon></div>
            <div class="account-profile__copy">
              <strong>{{ session.user?.name || '账户用户' }}</strong>
              <small>{{ session.user?.email || session.user?.phone || '已登录' }}</small>
            </div>
          </div>

          <div class="account-balance">
            <span>账户余额</span>
            <strong>￥{{ balance }}</strong>
            <RouterLink to="/user/recharge" @click="menuOpen = false">
              立即充值 <el-icon><ArrowRight /></el-icon>
            </RouterLink>
          </div>

          <div class="account-menu-label">账户中心</div>
          <RouterLink
            v-for="l in accountLinks"
            :key="l.to"
            :to="l.to"
            class="account-menu-item"
            :class="{ 'is-active': isActive(l) }"
            :aria-current="isActive(l) ? 'page' : undefined"
            @click="menuOpen = false"
          >
            <el-icon><component :is="l.icon" /></el-icon><span>{{ l.label }}</span>
          </RouterLink>

          <div class="account-menu-label">账户设置</div>
          <RouterLink
            v-for="l in settingLinks"
            :key="l.to"
            :to="l.to"
            class="account-menu-item"
            :class="{ 'is-active': isActive(l) }"
            :aria-current="isActive(l) ? 'page' : undefined"
            @click="menuOpen = false"
          >
            <el-icon><component :is="l.icon" /></el-icon><span>{{ l.label }}</span>
          </RouterLink>
        </div>
      </aside>

      <main class="account-content">
        <RouterView />
      </main>
    </div>
  </PublicContainer>
</template>

<style scoped>
.account-layout {
  display: grid;
  grid-template-columns: 236px minmax(0, 1fr);
  gap: 22px;
  align-items: start;
}

.account-sidebar {
  position: sticky;
  top: 84px;
  align-self: start;
}

.account-panel {
  padding: 14px;
}

.account-profile {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 7px 16px;
  border-bottom: 1px solid var(--art-card-border);
}

.account-profile__avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  color: var(--theme-color-contrast);
  font-size: 17px;
  background: var(--theme-color);
  border-radius: var(--radius-md);
}

.account-profile__copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.account-profile__copy strong {
  overflow: hidden;
  color: var(--art-gray-800);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account-profile__copy small {
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account-balance {
  margin: 14px 0;
  padding: 13px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.account-balance span {
  display: block;
  color: var(--art-gray-500);
  font-size: 11px;
}

.account-balance strong {
  display: block;
  margin: 4px 0 8px;
  color: var(--theme-color);
  font-size: 20px;
  letter-spacing: -0.03em;
}

.account-balance a {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  color: var(--theme-color);
  font-size: 12px;
  font-weight: 600;
}

.account-menu-label {
  margin: 15px 8px 6px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 600;
}

.account-menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 38px;
  margin: 2px 0;
  padding: 0 10px;
  color: var(--art-gray-600);
  font-size: 13px;
  border-radius: var(--radius-md);
}

.account-menu-item:hover {
  color: var(--theme-color);
  background: var(--art-hover-color);
}

.account-menu-item.is-active {
  color: var(--theme-color);
  font-weight: 600;
  background: var(--theme-color-soft);
}

.account-menu-item .el-icon {
  color: var(--art-gray-400);
  font-size: 15px;
}

.account-menu-item.is-active .el-icon {
  color: var(--theme-color);
}

.account-content {
  min-width: 0;
}

.account-mobile-bar {
  display: none;
}

@media (max-width: 800px) {
  .account-layout {
    display: block;
  }

  .account-mobile-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    margin-bottom: 12px;
    padding: 12px 14px;
    font: inherit;
    text-align: left;
    background: var(--default-box-color);
    border: 1px solid var(--art-card-border);
    border-radius: var(--radius-lg);
    cursor: pointer;
  }

  .account-mobile-bar__copy {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .account-mobile-bar__copy strong {
    color: var(--art-gray-800);
    font-size: 13px;
  }

  .account-mobile-bar__copy small {
    color: var(--art-gray-500);
    font-size: 11px;
  }

  .account-mobile-bar__balance {
    color: var(--theme-color);
    font-size: 13px;
    font-weight: 600;
  }

  .account-mobile-bar__balance span {
    margin-left: 7px;
    color: var(--art-gray-500);
    font-size: 11px;
    font-weight: 400;
  }

  .account-sidebar {
    position: static;
    display: none;
  }

  .account-sidebar.is-open {
    display: block;
    margin-bottom: 15px;
  }
}
</style>
