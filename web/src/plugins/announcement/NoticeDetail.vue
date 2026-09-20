<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, Clock, Document } from '@element-plus/icons-vue'
import { fetchNotice, type Announcement } from '@/api/store'
import { formatDate } from '@/utils/format'
import PublicContainer from '@/components/public/PublicContainer.vue'

const route = useRoute()
const data = ref<Announcement | null>(null)
const loading = ref(false)
const loadError = ref(false)

async function load() {
  loading.value = true
  loadError.value = false
  try {
    data.value = await fetchNotice(String(route.params.id))
  } catch (err: unknown) {
    data.value = null
    loadError.value = true
    ElMessage.error((err as Error).message || '公告加载失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(() => route.params.id, load)
</script>

<template>
  <PublicContainer>
    <div class="notice-page">
      <template v-if="data">
        <header class="art-card notice-hero">
          <div class="notice-hero__tags">
            <span v-if="data.category" class="notice-hero__cat">{{ data.category }}</span>
            <span v-if="data.pinned" class="notice-hero__pin">置顶</span>
          </div>

          <h1>{{ data.title }}</h1>

          <div class="notice-hero__info">
            <span><el-icon><Clock /></el-icon>{{ formatDate(data.created_at) }}</span>
            <span class="notice-hero__dot" />
            <span>阅读 {{ data.reads ?? 0 }}</span>
          </div>

          <img v-if="data.cover" :src="data.cover" :alt="data.title" class="notice-hero__cover" />
        </header>

        <article class="art-card notice-content">{{ data.content }}</article>

        <div class="notice-foot">
          <RouterLink to="/notices" class="notice-foot__btn">
            <el-icon><ArrowLeft /></el-icon>返回公告列表
          </RouterLink>
        </div>
      </template>

      <div v-else-if="loadError" class="art-card notice-state" role="alert">
        <p>公告加载失败，请稍后重试。</p>
        <el-button size="small" @click="load">重新加载</el-button>
      </div>

      <div v-else-if="!loading" class="art-card notice-state">
        <el-icon class="notice-state__icon"><Document /></el-icon>
        <p>公告不存在或已下线</p>
        <RouterLink to="/notices" class="notice-state__link">看看其它公告</RouterLink>
      </div>
    </div>
  </PublicContainer>
</template>

<style scoped>
.notice-page {
  max-width: 880px;
  margin: 0 auto;
}

.notice-hero {
  padding: 30px 32px;
}

.notice-hero__tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.notice-hero__cat {
  padding: 3px 10px;
  color: var(--theme-color);
  font-size: 12px;
  font-weight: 600;
  background: var(--theme-color-soft);
  border-radius: var(--radius-sm);
}

.notice-hero__pin {
  padding: 3px 10px;
  color: var(--el-color-danger);
  font-size: 12px;
  font-weight: 600;
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-sm);
}

.notice-hero h1 {
  margin: 14px 0 0;
  color: var(--art-gray-900);
  font-size: 28px;
  font-weight: 700;
  line-height: 1.35;
  letter-spacing: -0.02em;
}

.notice-hero__info {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 16px;
  color: var(--art-gray-500);
  font-size: 13px;
}

.notice-hero__info span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.notice-hero__info .el-icon {
  color: var(--art-gray-400);
  font-size: 14px;
}

.notice-hero__dot {
  width: 4px;
  height: 4px;
  background: var(--art-gray-300);
  border-radius: 50%;
}

.notice-hero__cover {
  width: 100%;
  max-height: 380px;
  margin-top: 22px;
  object-fit: cover;
  border-radius: var(--radius-md);
}

.notice-content {
  margin-top: 16px;
  padding: 30px 32px;
  color: var(--art-gray-700);
  font-size: 15px;
  line-height: 1.95;
  white-space: pre-wrap;
  word-break: break-word;
}

.notice-foot {
  display: flex;
  justify-content: center;
  margin-top: 22px;
}

.notice-foot__btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 42px;
  padding: 0 22px;
  color: var(--theme-color);
  font-size: 14px;
  font-weight: 600;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
  transition: background 0.16s ease, color 0.16s ease;
}

.notice-foot__btn:hover {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
}

.notice-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 72px 24px;
  text-align: center;
}

.notice-state__icon {
  margin-bottom: 6px;
  color: var(--art-gray-300);
  font-size: 44px;
}

.notice-state p {
  margin: 0;
  color: var(--art-gray-700);
  font-size: 15px;
  font-weight: 600;
}

.notice-state__link {
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
}

.notice-state__link:hover {
  text-decoration: underline;
}

@media (max-width: 640px) {
  .notice-hero {
    padding: 22px 18px;
  }

  .notice-hero h1 {
    font-size: 22px;
  }

  .notice-content {
    padding: 22px 18px;
    font-size: 14px;
  }
}
</style>
