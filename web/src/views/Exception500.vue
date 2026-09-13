<!-- 500 服务器错误 -->
<template>
  <ArtException :data="{ title: '500', desc, btnText, imgUrl }" />
</template>

<script setup lang="ts">
  import { $t } from '@/locales'
  import imgUrl from '@imgs/svg/500.svg'

  defineOptions({ name: 'Exception500' })

  const desc = $t('exceptionPage.500')
  const btnText = $t('exceptionPage.gohome')
</script>
