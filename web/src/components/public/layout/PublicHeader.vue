<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowDown,
  User,
  Box,
  Wallet,
  CircleClose,
  Monitor,
  Moon,
  Sunny,
  Bell,
} from '@element-plus/icons-vue'
import { useSession } from '@/http/session'
import { http } from '@/http'
import {
  unreadNotifications,
  refreshUnreadNotifications,
} from '@/components/public/useUnreadNotifications'
import { togglePublicTheme, isPublicDark } from '@/components/public/usePublicTheme'
import PublicContainer from '@/components/public/PublicContainer.vue'
import HeaderMegaMenu from './HeaderMegaMenu.vue'

defineOptions({ name: 'PublicHeader' })

const session = useSession()
const route = useRoute()
const router = useRouter()

const scrolled = ref(false)
const dark = computed(() => isPublicDark())
const activeMenu = ref<'' | 'products' | 'notices' | 'other'>('')
const userMenuOpen = ref(false)
const mobileOpen = ref(false)

let closeTimer: ReturnType<typeof setTimeout> | null = null
function openMenu(menu: 'products' | 'notices' | 'other') {
  if (closeTimer) clearTimeout(closeTimer)
  activeMenu.value = menu
}
function scheduleClose() {
  if (closeTimer) clearTimeout(closeTimer)
  closeTimer = setTimeout(() => (activeMenu.value = ''), 160)
}
function keepMenu() {
  if (closeTimer) clearTimeout(closeTimer)
}

function onScroll() {
  scrolled.value = window.scrollY > 8
}

watch(
  () => route.fullPath,
  () => {
    activeMenu.value = ''
    userMenuOpen.value = false
    mobileOpen.value = false
    // 切页面时重取未读数：其它页面（消息中心）可能刚把消息标记为已读。
    void refreshUnreadNotifications()
  },
)

function toggleTheme(e: MouseEvent) {
  // 传事件：以点击位置为圆心做圆形扩散过渡，与登录页顶栏一致
  togglePublicTheme(e)
}

async function doLogout() {
  try {
    await http.post('/logout')
  } finally {
    session.user = null
    location.href = '/'
  }
}
function onUserCommand(cmd: string) {
  userMenuOpen.value = false
  if (cmd === 'logout') {
    void doLogout()
    return
  }
  const map: Record<string, string> = {
    user: '/user',
    services: '/services',
    recharge: '/user/recharge',
  }
  if (map[cmd]) router.push(map[cmd])
}

const onWindowFocus = () => void refreshUnreadNotifications()

if (typeof window !== 'undefined') {
  window.addEventListener('scroll', onScroll, { passive: true })
  onScroll()
}
onMounted(() => {
  // 回到窗口时重取：离开期间可能已收到新消息，也可能已掉线（此时未读数归 0）。
  window.addEventListener('focus', onWindowFocus)
  void refreshUnreadNotifications()
})
onUnmounted(() => window.removeEventListener('focus', onWindowFocus))
// 登录/退出（含会话掉线后被置空）都重取未读数，避免残留上一身份的角标。
watch(() => session.user?.id, () => void refreshUnreadNotifications())
</script>

<template>
  <header class="site-header" :class="{ scrolled }" @mouseleave="scheduleClose()">
    <PublicContainer class="header-bar">
      <RouterLink to="/" class="logo" @click="activeMenu = ''">{{ session.site.name }}</RouterLink>

      <nav class="main-nav">
        <RouterLink to="/" class="main-nav__link">
          <span class="main-nav__text">首页</span>
          <el-icon class="main-nav__arrow is-ghost"><ArrowDown /></el-icon>
        </RouterLink>
        <button
          type="button"
          class="main-nav__link"
          :class="{ 'is-active': route.path.startsWith('/cart') || route.path.startsWith('/buy') }"
          @mouseenter="openMenu('products')"
          @click="router.push('/cart')"
        >
          <span class="main-nav__text">产品</span>
          <el-icon class="main-nav__arrow"><ArrowDown /></el-icon>
        </button>
        <RouterLink
          to="/promotions"
          class="main-nav__link"
          :class="{ 'is-active': route.path.startsWith('/promotion') }"
        >
          <span class="main-nav__text">活动</span>
          <el-icon class="main-nav__arrow is-ghost"><ArrowDown /></el-icon>
        </RouterLink>
        <button
          type="button"
          class="main-nav__link"
          :class="{ 'is-active': route.path.startsWith('/notices') }"
          @mouseenter="openMenu('notices')"
          @click="router.push('/notices')"
        >
          <span class="main-nav__text">公告</span>
          <el-icon class="main-nav__arrow"><ArrowDown /></el-icon>
        </button>
        <button
          type="button"
          class="main-nav__link"
          @mouseenter="openMenu('other')"
        >
          <span class="main-nav__text">其他</span>
          <el-icon class="main-nav__arrow"><ArrowDown /></el-icon>
        </button>
      </nav>

      <div class="header-actions">
        <button class="icon-btn" type="button" title="切换主题" @click="toggleTheme">
          <el-icon><Sunny v-if="dark" /><Moon v-else /></el-icon>
        </button>
        <RouterLink v-if="session.user" to="/notifications" class="icon-btn notification-link" title="消息中心">
          <el-icon><Bell /></el-icon>
          <b v-if="unreadNotifications" class="notification-badge">{{ unreadNotifications > 99 ? '99+' : unreadNotifications }}</b>
        </RouterLink>

        <div v-if="session.user" class="user-menu">
          <button class="user-trigger" type="button" @click="userMenuOpen = !userMenuOpen">
            <span class="user-avatar">{{ (session.user.name || session.user.email || 'U').charAt(0).toUpperCase() }}</span>
            <span class="user-name">{{ session.user.name || session.user.email }}</span>
            <el-icon class="user-arrow"><ArrowDown /></el-icon>
          </button>
          <Transition name="menu-fade">
            <div v-if="userMenuOpen" class="user-panel">
              <button type="button" class="user-item" @click="onUserCommand('user')"><el-icon><Monitor /></el-icon>账户中心</button>
              <button type="button" class="user-item" @click="onUserCommand('services')"><el-icon><Box /></el-icon>我的服务</button>
              <div class="user-divider" />
              <button type="button" class="user-item" @click="onUserCommand('recharge')"><el-icon><Wallet /></el-icon>账户充值</button>
              <button type="button" class="user-item is-danger" @click="onUserCommand('logout')"><el-icon><CircleClose /></el-icon>退出登录</button>
            </div>
          </Transition>
        </div>
        <template v-else>
          <RouterLink to="/login" class="header-link">登录</RouterLink>
          <RouterLink to="/register" class="header-register">免费注册</RouterLink>
        </template>

        <button class="icon-btn mobile-toggle" type="button" @click="mobileOpen = !mobileOpen">
          <span class="hamburger" :class="{ 'is-open': mobileOpen }"><i /><i /><i /></span>
        </button>
      </div>
    </PublicContainer>

    <!-- 产品 / 公告 / 其他 mega-menu -->
    <HeaderMegaMenu
      :active="activeMenu"
      @enter="keepMenu()"
      @leave="scheduleClose()"
      @close="activeMenu = ''"
    />

    <!-- 移动端抽屉 -->
    <Transition name="drawer">
      <div v-if="mobileOpen" class="mobile-drawer">
        <RouterLink to="/" @click="mobileOpen = false">首页</RouterLink>
        <RouterLink to="/cart" @click="mobileOpen = false">产品与服务</RouterLink>
        <RouterLink to="/promotions" @click="mobileOpen = false">营销活动</RouterLink>
        <RouterLink to="/notices" @click="mobileOpen = false">公告</RouterLink>
        <RouterLink v-if="session.user" to="/user" @click="mobileOpen = false">账户中心</RouterLink>
        <RouterLink v-if="session.user" to="/services" @click="mobileOpen = false">我的服务</RouterLink>
        <RouterLink v-else to="/login" @click="mobileOpen = false">登录</RouterLink>
      </div>
    </Transition>
  </header>
</template>

<style scoped>
.site-header {
  position: sticky;
  top: 0;
  z-index: 100;
  background: color-mix(in srgb, var(--default-box-color) 94%, transparent);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  border-bottom: 1px solid transparent;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.site-header.scrolled {
  border-bottom-color: var(--art-card-border);
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.05);
}

.header-bar {
  display: flex;
  align-items: center;
  height: 64px;
  gap: 0;
}

.logo {
  flex-shrink: 0;
  color: var(--art-gray-900);
  font-size: 20px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.main-nav {
  display: flex;
  align-items: center;
  margin-left: 34px;
}

.main-nav__link {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 64px;
  padding: 0 20px;
  color: var(--art-gray-700);
  font-size: 14px;
  font-weight: 500;
  background: transparent;
  border: 0;
  cursor: pointer;
  transition: color 0.16s ease;
}

.main-nav__text {
  position: relative;
  /* 锁定行高，保证 ::after 的横条始终落在顶栏底部（链接高 64px，文字行高 21px） */
  line-height: 21px;
}

.main-nav__text::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: -21px;
  height: 2px;
  background: var(--theme-color);
  border-radius: 2px 2px 0 0;
  transform: scaleX(0);
  transform-origin: center;
  transition: transform 0.18s ease;
}

.main-nav__link:hover,
.main-nav__link.router-link-active,
.main-nav__link.is-active {
  color: var(--theme-color);
}

.main-nav__link.router-link-active .main-nav__text::after,
.main-nav__link.is-active .main-nav__text::after {
  transform: scaleX(1);
}

.main-nav__arrow {
  font-size: 11px;
  opacity: 0.55;
}

/* 首页无下拉，占位一个不可见箭头，保证各菜单项文字间距一致 */
.main-nav__arrow.is-ghost {
  visibility: hidden;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  flex-shrink: 0;
}

.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  color: var(--art-gray-600);
  font-size: 18px;
  background: transparent;
  border: 0;
  border-radius: 999px;
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease;
}

.icon-btn:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
}

.notification-link {
  position: relative;
}

.notification-badge {
  position: absolute;
  top: 1px;
  right: 0;
  min-width: 15px;
  padding: 1px 4px;
  color: #fff;
  font-size: 9px;
  line-height: 13px;
  background: var(--el-color-danger);
  border: 2px solid var(--default-box-color);
  border-radius: 999px;
}

.user-menu {
  position: relative;
}

.user-trigger {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px 4px 4px;
  background: transparent;
  border: 0;
  border-radius: 999px;
  cursor: pointer;
  transition: background 0.16s ease;
}

.user-trigger:hover {
  background: var(--theme-color-soft);
}

.user-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  background: var(--theme-color);
  border-radius: 50%;
}

.user-name {
  max-width: 120px;
  overflow: hidden;
  color: var(--art-gray-700);
  font-size: 13px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-arrow {
  font-size: 12px;
  color: var(--art-gray-400);
}

.user-panel {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  z-index: 120;
  min-width: 184px;
  padding: 6px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: 12px;
  box-shadow: 0 12px 32px rgba(15, 23, 42, 0.12);
}

.user-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 12px;
  color: var(--art-gray-700);
  font-size: 14px;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: 8px;
  cursor: pointer;
}

.user-item:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
}

.user-item.is-danger {
  color: var(--el-color-danger);
}

.user-divider {
  height: 1px;
  margin: 6px 4px;
  background: var(--art-card-border);
}

.header-link {
  display: inline-flex;
  align-items: center;
  height: 36px;
  padding: 0 14px;
  color: var(--art-gray-700);
  font-size: 13px;
  font-weight: 500;
  border-radius: 999px;
  transition: color 0.16s ease, background 0.16s ease;
}

.header-link:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
}

.header-register {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 36px;
  padding: 0 20px;
  margin-left: 4px;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  background: var(--theme-color);
  border-radius: 8px;
  box-shadow: 0 4px 12px color-mix(in srgb, var(--theme-color) 26%, transparent);
  transition: background 0.16s ease, box-shadow 0.16s ease;
}

.header-register:hover {
  background: var(--theme-color-deep);
  box-shadow: 0 8px 20px color-mix(in srgb, var(--theme-color) 32%, transparent);
}

.menu-fade-enter-active,
.menu-fade-leave-active,
.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.menu-fade-enter-from,
.menu-fade-leave-to {
  opacity: 0;
  transform: scaleY(0.9);
}

/* mobile */
.mobile-toggle {
  display: none;
}

.hamburger {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 18px;
}

.hamburger i {
  display: block;
  width: 18px;
  height: 2px;
  background: currentColor;
  border-radius: 1px;
  transition: transform 0.25s ease, opacity 0.25s ease;
}

.hamburger.is-open i:nth-child(1) {
  transform: translateY(6px) rotate(45deg);
}

.hamburger.is-open i:nth-child(2) {
  opacity: 0;
}

.hamburger.is-open i:nth-child(3) {
  transform: translateY(-6px) rotate(-45deg);
}

.mobile-drawer {
  display: none;
  flex-direction: column;
  padding: 8px 16px 16px;
  background: var(--default-box-color);
  border-top: 1px solid var(--art-card-border);
}

.mobile-drawer a {
  display: flex;
  align-items: center;
  padding: 13px 6px;
  color: var(--art-gray-700);
  font-size: 15px;
  border-bottom: 1px solid var(--art-card-border);
  transition: color 0.16s ease;
}

.mobile-drawer a:hover,
.mobile-drawer a.router-link-active {
  color: var(--theme-color);
}

.mobile-drawer a:last-child {
  border-bottom: 0;
}

@media (max-width: 960px) {
  .main-nav {
    display: none;
  }

  .mobile-toggle {
    display: inline-flex;
  }

  .mobile-drawer {
    display: flex;
  }

  .user-name {
    display: none;
  }
}
</style>
