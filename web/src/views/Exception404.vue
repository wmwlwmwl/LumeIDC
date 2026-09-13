<!-- 404 页面不存在 -->
<template>
  <ArtException :data="{ title: '404', desc, btnText, imgUrl }" />
</template>

<script setup lang="ts">
  import { $t } from '@/locales'
  import imgUrl from '@imgs/svg/404.svg'

  defineOptions({ name: 'Exception404' })

  const desc = $t('exceptionPage.404')
  const btnText = $t('exceptionPage.gohome')
</script>
