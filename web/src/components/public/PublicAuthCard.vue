<script setup lang="ts">
  defineOptions({ name: 'PublicAuthCard' })

  defineProps<{
    title: string
    subtitle?: string
    wide?: boolean
  }>()
</script>

<template>
  <section class="art-card auth-main" :class="{ 'is-wide': wide }" :aria-label="title">
    <h1 class="auth-title">{{ title }}</h1>
    <p v-if="subtitle" class="auth-subtitle">{{ subtitle }}</p>
    <div class="auth-body">
      <slot />
    </div>
  </section>
</template>

<style scoped>
  .auth-main {
    box-sizing: border-box;
    width: min(100%, 440px);
    padding: 36px 32px 30px;
  }

  .auth-main.is-wide {
    width: min(100%, 560px);
  }

  .auth-title {
    margin: 0;
    color: var(--art-gray-900);
    font-size: 30px;
    font-weight: 700;
    letter-spacing: -0.03em;
  }

  .auth-subtitle {
    margin: 8px 0 0;
    color: var(--art-gray-500);
    font-size: 14px;
  }

  .auth-body {
    margin-top: 24px;
  }

  @media (max-width: 640px) {
    .auth-main {
      padding: 28px 20px 24px;
    }

    .auth-title {
      font-size: 26px;
    }
  }
</style>
