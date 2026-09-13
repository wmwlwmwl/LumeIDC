<script setup lang="ts">
  defineOptions({ name: 'PublicSectionHeader' })

  defineProps<{
    title: string
    subtitle?: string
  }>()
</script>

<template>
  <div class="public-section-head">
    <div class="public-section-head__copy">
      <h2>{{ title }}</h2>
      <p v-if="subtitle">{{ subtitle }}</p>
    </div>
    <div v-if="$slots.extra" class="public-section-head__extra">
      <slot name="extra" />
    </div>
  </div>
</template>

<style scoped>
  .public-section-head {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 18px;
  }

  .public-section-head__copy h2 {
    margin: 0;
    color: var(--art-gray-900);
    font-size: 18px;
    font-weight: 600;
  }

  .public-section-head__copy p {
    margin: 4px 0 0;
    color: var(--art-gray-500);
    font-size: 13px;
  }

  @media (max-width: 640px) {
    .public-section-head {
      align-items: flex-start;
      flex-direction: column;
    }
  }
</style>
