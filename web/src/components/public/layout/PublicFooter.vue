<script setup lang="ts">
import { useSession } from '@/http/session'
import PublicContainer from '@/components/public/PublicContainer.vue'

defineOptions({ name: 'PublicFooter' })

const session = useSession()
</script>

<template>
  <footer class="site-footer">
    <PublicContainer>
      <div class="footer-top">
        <div class="footer-brand">
          <div class="footer-logo">{{ session.site.name }}</div>
          <p class="footer-brand__desc">
            {{ session.site.description || '为企业与开发者提供稳定、安全、高性价比的云计算与 IDC 服务。' }}
          </p>
          <ul class="footer-contact">
            <li v-if="session.site.email"><span>邮箱</span><a :href="`mailto:${session.site.email}`">{{ session.site.email }}</a></li>
            <li v-if="session.site.phone"><span>电话</span><b>{{ session.site.phone }}</b></li>
            <li v-if="session.site.hours"><span>服务时间</span><b>{{ session.site.hours }}</b></li>
          </ul>
        </div>

        <div class="footer-columns">
          <div class="footer-col">
            <h4>产品</h4>
            <RouterLink to="/cart">全部产品</RouterLink>
            <RouterLink to="/cart">云服务器</RouterLink>
            <RouterLink to="/cart">弹性计算</RouterLink>
          </div>
          <div class="footer-col">
            <h4>支持</h4>
            <RouterLink to="/notices">站点公告</RouterLink>
            <a v-if="session.site.email" :href="`mailto:${session.site.email}`">联系我们</a>
          </div>
          <div class="footer-col">
            <h4>账户</h4>
            <RouterLink to="/register">免费注册</RouterLink>
            <RouterLink to="/login">账户登录</RouterLink>
            <RouterLink to="/user">账户中心</RouterLink>
            <RouterLink to="/user/verification">实名认证</RouterLink>
          </div>
        </div>
      </div>

      <div class="footer-bottom">
        <p>© {{ new Date().getFullYear() }} {{ session.site.name }}. All rights reserved.</p>
        <p>{{ session.site.keywords || '云基础设施服务' }}</p>
      </div>
    </PublicContainer>
  </footer>
</template>

<style scoped>
.site-footer {
  margin-top: 64px;
  background: var(--default-box-color);
  border-top: 1px solid var(--art-card-border);
}

.footer-top {
  display: grid;
  grid-template-columns: minmax(0, 1.2fr) minmax(0, 1.6fr);
  gap: 40px;
  padding: 48px 0 32px;
}

.footer-logo {
  color: var(--art-gray-900);
  font-size: 20px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.footer-brand__desc {
  max-width: 380px;
  margin: 14px 0 0;
  color: var(--art-gray-500);
  font-size: 13px;
  line-height: 1.9;
}

.footer-contact {
  margin: 20px 0 0;
  padding: 0;
  list-style: none;
}

.footer-contact li {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 4px 0;
  font-size: 13px;
}

.footer-contact span {
  color: var(--art-gray-400);
  min-width: 60px;
}

.footer-contact b,
.footer-contact a {
  color: var(--art-gray-700);
}

.footer-contact a:hover {
  color: var(--theme-color);
}

.footer-columns {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 24px;
}

.footer-col {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.footer-col h4 {
  margin: 0 0 4px;
  color: var(--art-gray-900);
  font-size: 14px;
  font-weight: 600;
}

.footer-col a {
  color: var(--art-gray-500);
  font-size: 13px;
}

.footer-col a:hover {
  color: var(--theme-color);
}

.footer-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 18px 0 26px;
  color: var(--art-gray-400);
  font-size: 12px;
  border-top: 1px solid var(--art-card-border);
}

.footer-bottom p {
  margin: 0;
}

@media (max-width: 800px) {
  .footer-top {
    grid-template-columns: 1fr;
    gap: 28px;
    padding: 34px 0 24px;
  }

  .footer-columns {
    grid-template-columns: repeat(2, 1fr);
  }

  .footer-bottom {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
