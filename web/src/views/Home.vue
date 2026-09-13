<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { fetchHome, type HomeData } from '../api/store'
import { useSession } from '../http/session'
import HomeHero from './Home/HomeHero.vue'
import HomeProductTabs from './Home/HomeProductTabs.vue'
import HomeSolutions from './Home/HomeSolutions.vue'
import HomeNews from './Home/HomeNews.vue'
import HomeRegisterBar from './Home/HomeRegisterBar.vue'

const session = useSession()
const home = reactive<HomeData>({ catalog: [], products: [], announcements: [] })
const loading = ref(false)
const loadError = ref(false)

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const data = await fetchHome()
    home.catalog = data.catalog
    home.products = data.products
    home.announcements = data.announcements
  } catch {
    loadError.value = true
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="home-page">
    <el-skeleton v-if="loading && !home.catalog.length" :rows="8" animated class="home-loading" />
    <template v-else>
      <div v-if="loadError" class="home-error" role="alert">
        <span>首页内容暂时加载失败，可重试或稍后再访问。</span>
        <button type="button" class="home-error__retry" @click="load">重新加载</button>
      </div>
      <HomeHero :catalog="home.catalog" :description="session.site.description" />
      <HomeProductTabs :products="home.products" />
      <HomeSolutions />
      <HomeNews :notices="home.announcements" />
      <HomeRegisterBar />
    </template>
  </div>
</template>

<style scoped>
.home-page {
  display: flex;
  flex-direction: column;
  padding-bottom: 24px;
}

.home-loading {
  box-sizing: border-box;
  width: 100%;
  margin: 40px 0;
  padding: 0 32px;
}

.home-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  box-sizing: border-box;
  margin: 20px 32px 0;
  padding: 14px 18px;
  color: var(--el-color-danger);
  font-size: 13px;
  background: var(--el-color-danger-light-9);
  border: 1px solid color-mix(in srgb, var(--el-color-danger) 24%, transparent);
  border-radius: var(--radius-md);
}

.home-error__retry {
  flex-shrink: 0;
  padding: 6px 14px;
  color: var(--el-color-danger);
  font-size: 13px;
  font-weight: 600;
  background: transparent;
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.home-error__retry:hover {
  color: var(--theme-color-contrast);
  background: var(--el-color-danger);
}

@media (max-width: 900px) {
  .home-loading {
    padding: 0 20px;
  }

  .home-error {
    margin: 20px 20px 0;
  }
}

@media (max-width: 640px) {
  .home-loading {
    padding: 0 16px;
  }

  .home-error {
    margin: 16px 16px 0;
  }
}
</style>
