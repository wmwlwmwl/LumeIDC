<script setup lang="ts">
import { RouterView } from 'vue-router'
import PublicHeader from '@/components/public/layout/PublicHeader.vue'
import PublicFooter from '@/components/public/layout/PublicFooter.vue'

defineOptions({ name: 'PublicLayout' })
</script>

<template>
  <div class="public-shell">
    <PublicHeader />
    <main class="public-main">
      <RouterView v-slot="{ Component }">
        <Transition name="page-fade" mode="out-in">
          <component :is="Component" />
        </Transition>
      </RouterView>
    </main>
    <PublicFooter />
  </div>
</template>

<style scoped>
.public-shell {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  background: var(--default-bg-color);
}

.public-main {
  flex: 1;
  padding: 26px 0 0;
}

.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.page-fade-enter-from {
  opacity: 0;
  transform: translateY(6px);
}

.page-fade-leave-to {
  opacity: 0;
}
</style>
