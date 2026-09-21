<script setup lang="ts">
import { computed } from 'vue'
import { useSession } from '@/http/session'
import PublicContainer from '@/components/public/PublicContainer.vue'

defineOptions({ name: 'HomeRegisterBar' })

const session = useSession()
const loggedIn = computed(() => !!session.user)
</script>

<template>
  <section class="regbar-section">
    <PublicContainer>
      <div class="regbar">
        <div class="regbar__copy">
          <h2>{{ loggedIn ? '继续管理你的云资源' : '现在注册，开启您的云上之旅！' }}</h2>
          <p>{{ loggedIn ? '前往控制台查看服务、账单与余额。' : '注册后即可选购产品、在线支付并即时开通。' }}</p>
        </div>
        <RouterLink :to="loggedIn ? '/user' : '/register'" class="regbar__btn">
          {{ loggedIn ? '进入账户中心' : '立即注册' }}
        </RouterLink>
      </div>
    </PublicContainer>
  </section>
</template>

<style scoped>
.regbar-section {
  padding: 24px 0 0;
}

.regbar {
  position: relative;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 34px 40px;
  color: var(--theme-color-contrast);
  background: linear-gradient(120deg, var(--theme-color), var(--theme-color-deep));
  border-radius: var(--radius-lg);
  box-shadow: 0 24px 48px color-mix(in srgb, var(--theme-color) 26%, transparent);
}

.regbar::before {
  content: '';
  position: absolute;
  top: -60%;
  right: -8%;
  width: 420px;
  height: 420px;
  background: radial-gradient(circle, color-mix(in srgb, #fff 22%, transparent), transparent 65%);
  pointer-events: none;
}

.regbar__copy,
.regbar__btn {
  position: relative;
  z-index: 1;
}

.regbar__copy h2 {
  margin: 0;
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.regbar__copy p {
  margin: 8px 0 0;
  color: color-mix(in srgb, var(--theme-color-contrast) 80%, transparent);
  font-size: 13px;
}

.regbar__btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 46px;
  padding: 0 32px;
  color: var(--theme-color-deep);
  font-size: 15px;
  font-weight: 700;
  background: var(--theme-color-contrast);
  border-radius: var(--radius-md);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

.regbar__btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 12px 24px rgba(0, 0, 0, 0.18);
}

@media (max-width: 720px) {
  .regbar {
    flex-direction: column;
    align-items: stretch;
    padding: 26px 24px;
  }

  .regbar__copy h2 {
    font-size: 19px;
  }

  .regbar__btn {
    width: 100%;
  }
}
</style>
