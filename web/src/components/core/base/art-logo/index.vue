<!-- 站点标识占位：LumeIDC 不使用 Art 的 Logo，也不做单字占位。
     站点名称由相邻的完整文字（系统名称）展示。 -->
<template>
  <span class="art-logo-mark" aria-hidden="true" />
</template>

<script setup lang="ts">
  defineOptions({ name: 'ArtLogo' })

  interface Props {
    /** 兼容 Art 调用方传入的尺寸参数 */
    size?: number | string
  }

  defineProps<Props>()
</script>

<style scoped>
  .art-logo-mark {
    display: none;
  }
</style>
