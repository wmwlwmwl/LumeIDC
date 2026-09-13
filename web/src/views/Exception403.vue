<!-- 403 无权限页面 -->
<template>
  <ArtException :data="{ title: '403', desc, btnText, imgUrl }" />
</template>

<script setup lang="ts">
  import { $t } from '@/locales'
  import imgUrl from '@imgs/svg/403.svg'

  defineOptions({ name: 'Exception403' })

  const desc = $t('exceptionPage.403')
  const btnText = $t('exceptionPage.gohome')
</script>
