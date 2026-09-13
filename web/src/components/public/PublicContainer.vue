<script setup lang="ts">
  defineOptions({ name: 'PublicContainer' })
</script>

<template>
  <div class="public-container">
    <slot />
  </div>
</template>

<style scoped>
  .public-container {
    box-sizing: border-box;
    width: 100%;
    max-width: 1280px;
    margin: 0 auto;
    padding: 0 32px;
  }

  @media (max-width: 900px) {
    .public-container {
      padding: 0 20px;
    }
  }

  @media (max-width: 640px) {
    .public-container {
      padding: 0 16px;
    }
  }
</style>
