<script setup lang="ts">
  defineOptions({ name: 'PublicPageHead' })

  defineProps<{
    title: string
    subtitle?: string
  }>()
</script>

<template>
  <div class="art-card public-page-head">
    <div class="public-page-head__copy">
      <h1>{{ title }}</h1>
      <p v-if="subtitle">{{ subtitle }}</p>
    </div>
    <div v-if="$slots.extra" class="public-page-head__extra">
      <slot name="extra" />
    </div>
  </div>
</template>

<style scoped>
  .public-page-head {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 18px;
    padding: 16px 20px;
  }

  .public-page-head__copy h1 {
    margin: 0;
    color: var(--art-gray-900);
    font-size: 18px;
    font-weight: 600;
  }

  .public-page-head__copy p {
    margin: 4px 0 0;
    color: var(--art-gray-500);
    font-size: 13px;
  }

  @media (max-width: 640px) {
    .public-page-head {
      align-items: flex-start;
      flex-direction: column;
    }
  }
</style>
