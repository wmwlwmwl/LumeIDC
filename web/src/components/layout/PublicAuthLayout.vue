<script setup lang="ts">
import { RouterView } from 'vue-router'
import LoginLeftView from '@/components/core/views/login/LoginLeftView.vue'
import AuthTopBar from '@/components/core/views/login/AuthTopBar.vue'

defineOptions({ name: 'PublicAuthLayout' })
</script>

<template>
  <div class="public-auth">
    <LoginLeftView />

    <div class="public-auth__right">
      <AuthTopBar />
      <RouterView />
    </div>
  </div>
</template>

<style scoped>
.public-auth {
  display: flex;
  width: 100%;
  height: 100vh;
}

.public-auth__right {
  position: relative;
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px 20px;
  overflow: auto;
}

@media (max-width: 640px) {
  .public-auth__right {
    padding: 72px 24px 32px;
  }
}
</style>
