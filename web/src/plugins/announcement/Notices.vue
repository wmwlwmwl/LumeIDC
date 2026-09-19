<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Search, Document, ArrowRight, ChatDotRound, Clock } from '@element-plus/icons-vue'
import { fetchNotices, type Announcement } from '@/api/store'
import { formatDate } from '@/utils/format'
import PublicContainer from '@/components/public/PublicContainer.vue'
import PublicPageHead from '@/components/public/PublicPageHead.vue'

const list = ref<Announcement[]>([])
const categories = ref<string[]>([])
const total = ref(0)
const page = ref(1)
const limit = ref(10)
const category = ref('')
const keyword = ref('')
const loading = ref(false)
const loadError = ref(false)

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const data = await fetchNotices({
      category: category.value,
      keyword: keyword.value.trim(),
      page: page.value,
      limit: limit.value,
    })
    list.value = data.list || []
    categories.value = data.categories || []
    total.value = data.total || 0
  } catch (err: unknown) {
    loadError.value = true
    ElMessage.error((err as Error).message || '公告加载失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function pickCategory(c: string) {
  category.value = c
  page.value = 1
  load()
}
function search() {
  page.value = 1
  load()
}
function onPage(p: number) {
  page.value = p
  load()
}
function excerpt(n: Announcement): string {
  return n.summary || n.content || ''
}
</script>

<template>
  <PublicContainer>
    <PublicPageHead title="站点公告" subtitle="产品更新、活动与维护通知，第一时间掌握平台动态。" />

    <div class="notices-layout">
      <div class="notices-main">
        <div class="art-card notices-toolbar">
          <div class="notices-cats">
            <button type="button" class="cat-tab" :class="{ 'is-active': category === '' }" @click="pickCategory('')">
              全部
            </button>
            <button
              v-for="c in categories"
              :key="c"
              type="button"
              class="cat-tab"
              :class="{ 'is-active': category === c }"
              @click="pickCategory(c)"
            >
              {{ c }}
            </button>
          </div>
          <el-input
            v-model="keyword"
            class="notices-search"
            aria-label="搜索公告标题"
            placeholder="搜索公告标题"
            clearable
            @keyup.enter="search"
            @clear="search"
          >
            <template #prefix><el-icon><Search /></el-icon></template>
            <template #append><el-button @click="search">搜索</el-button></template>
          </el-input>
        </div>

        <div class="notices-list">
          <template v-if="loading && !list.length">
            <div v-for="i in 4" :key="i" class="art-card notice-item is-skeleton">
              <span class="skeleton skeleton--thumb" />
              <div class="notice-item__body">
                <span class="skeleton skeleton--line" style="width: 34%" />
                <span class="skeleton skeleton--line" style="width: 78%; height: 18px" />
                <span class="skeleton skeleton--line" style="width: 92%" />
                <span class="skeleton skeleton--line" style="width: 60%" />
              </div>
            </div>
          </template>

          <template v-else>
            <RouterLink
              v-for="n in list"
              :key="n.id"
              :to="`/notices/${n.id}`"
              class="art-card notice-item"
            >
              <div class="notice-item__thumb">
                <img v-if="n.cover" :src="n.cover" :alt="n.title" />
                <el-icon v-else><Document /></el-icon>
              </div>

              <div class="notice-item__body">
                <div class="notice-item__top">
                  <span v-if="n.pinned" class="notice__pin">置顶</span>
                  <span v-if="n.category" class="notice__cat">{{ n.category }}</span>
                  <time :datetime="n.created_at">{{ formatDate(n.created_at) }}</time>
                </div>
                <h3 class="notice-item__title">{{ n.title }}</h3>
                <p v-if="excerpt(n)" class="notice-item__excerpt">{{ excerpt(n) }}</p>
                <div class="notice-item__foot">
                  <span class="notice-item__reads">
                    <el-icon><Clock /></el-icon>阅读 {{ n.reads ?? 0 }}
                  </span>
                  <span class="notice-item__more">阅读全文 <el-icon><ArrowRight /></el-icon></span>
                </div>
              </div>
            </RouterLink>
          </template>

          <div v-if="loadError" class="art-card notices-state" role="alert">
            <p>公告加载失败，请稍后重试。</p>
            <el-button size="small" @click="load">重新加载</el-button>
          </div>
          <div v-else-if="!list.length && !loading" class="art-card notices-state">
            <el-icon class="notices-state__icon"><Document /></el-icon>
            <p>{{ keyword || category ? '没有匹配的公告' : '暂无公告' }}</p>
            <span>{{ keyword || category ? '换个关键词或分类试试。' : '有新的公告时会展示在这里。' }}</span>
          </div>
        </div>

        <div v-if="total > limit" class="notices-pager">
          <el-pagination
            layout="prev, pager, next"
            :total="total"
            :page-size="limit"
            :current-page="page"
            background
            @current-change="onPage"
          />
        </div>
      </div>

      <aside class="notices-side">
        <div class="art-card side-card">
          <h3>公告分类</h3>
          <nav class="side-cats">
            <button
              type="button"
              class="side-cat"
              :class="{ 'is-active': category === '' }"
              @click="pickCategory('')"
            >
              <span>全部公告</span>
              <el-icon><ArrowRight /></el-icon>
            </button>
            <button
              v-for="c in categories"
              :key="c"
              type="button"
              class="side-cat"
              :class="{ 'is-active': category === c }"
              @click="pickCategory(c)"
            >
              <span>{{ c }}</span>
              <el-icon><ArrowRight /></el-icon>
            </button>
          </nav>
        </div>

        <div class="art-card side-card side-help">
          <span class="side-help__icon"><el-icon><ChatDotRound /></el-icon></span>
          <h3>需要帮助？</h3>
          <p>对公告内容有疑问，或需要人工协助，可提交工单联系客服。</p>
          <RouterLink to="/tickets" class="side-help__btn">提交工单</RouterLink>
        </div>
      </aside>
    </div>
  </PublicContainer>
</template>

<style scoped>
.notices-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 292px;
  gap: 18px;
  align-items: start;
}

.notices-main {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}

.notices-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 16px;
}

.notices-cats {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.cat-tab {
  padding: 7px 16px;
  color: var(--art-gray-600);
  font-size: 13px;
  background: var(--art-gray-100);
  border: 1px solid transparent;
  border-radius: 999px;
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease, border-color 0.16s ease;
}

.cat-tab:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
}

.cat-tab.is-active {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
}

.notices-search {
  max-width: 320px;
}

.notices-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.notice-item {
  display: flex;
  gap: 18px;
  padding: 20px 22px;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.notice-item:hover {
  transform: translateY(-2px);
  border-color: color-mix(in srgb, var(--theme-color) 32%, var(--art-card-border));
  box-shadow: 0 14px 30px color-mix(in srgb, var(--art-gray-900) 8%, transparent);
}

.notice-item__thumb {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 132px;
  height: 92px;
  overflow: hidden;
  color: var(--theme-color);
  font-size: 30px;
  background: linear-gradient(
    135deg,
    var(--theme-color-soft),
    color-mix(in srgb, var(--theme-color) 6%, var(--default-box-color))
  );
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
}

.notice-item__thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.notice-item__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.notice-item__top {
  display: flex;
  align-items: center;
  gap: 8px;
}

.notice-item__top time {
  margin-left: auto;
  flex-shrink: 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.notice-item__title {
  margin: 10px 0 0;
  overflow: hidden;
  color: var(--art-gray-900);
  font-size: 16px;
  font-weight: 600;
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.notice-item:hover .notice-item__title {
  color: var(--theme-color);
}

.notice-item__excerpt {
  margin: 8px 0 0;
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 13px;
  line-height: 1.75;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

.notice-item__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: auto;
  padding-top: 14px;
}

.notice-item__reads {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--art-gray-400);
  font-size: 12px;
}

.notice-item__more {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
}

.notice-item__more .el-icon {
  transition: transform 0.16s ease;
}

.notice-item:hover .notice-item__more .el-icon {
  transform: translateX(3px);
}

.notice__pin {
  padding: 2px 8px;
  flex-shrink: 0;
  color: var(--el-color-danger);
  font-size: 11px;
  font-weight: 600;
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-sm);
}

.notice__cat {
  padding: 2px 8px;
  flex-shrink: 0;
  color: var(--theme-color);
  font-size: 11px;
  font-weight: 600;
  background: var(--theme-color-soft);
  border-radius: var(--radius-sm);
}

.notice-item.is-skeleton {
  pointer-events: none;
}

.skeleton {
  display: block;
  background: var(--art-gray-200);
  border-radius: var(--radius-sm);
  animation: notice-pulse 1.4s ease-in-out infinite;
}

.skeleton--thumb {
  width: 132px;
  height: 92px;
  flex-shrink: 0;
  border-radius: var(--radius-md);
}

.skeleton--line {
  height: 13px;
  margin-bottom: 9px;
}

@keyframes notice-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.45;
  }
}

.notices-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 64px 24px;
  text-align: center;
}

.notices-state__icon {
  margin-bottom: 6px;
  color: var(--art-gray-300);
  font-size: 40px;
}

.notices-state p {
  margin: 0;
  color: var(--art-gray-700);
  font-size: 15px;
  font-weight: 600;
}

.notices-state span {
  color: var(--art-gray-500);
  font-size: 13px;
}

.notices-pager {
  display: flex;
  justify-content: center;
  margin-top: 8px;
}

/* sidebar */
.notices-side {
  position: sticky;
  top: 84px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.side-card {
  padding: 18px;
}

.side-card h3 {
  margin: 0 0 12px;
  padding-left: 10px;
  color: var(--art-gray-900);
  font-size: 14px;
  font-weight: 700;
  border-left: 3px solid var(--theme-color);
  line-height: 1.2;
}

.side-cats {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.side-cat {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 9px 12px;
  color: var(--art-gray-600);
  font-size: 13px;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease;
}

.side-cat .el-icon {
  color: var(--art-gray-300);
  font-size: 12px;
  transition: transform 0.16s ease, color 0.16s ease;
}

.side-cat:hover {
  color: var(--theme-color);
  background: var(--art-hover-color);
}

.side-cat:hover .el-icon {
  transform: translateX(2px);
  color: var(--theme-color);
}

.side-cat.is-active {
  color: var(--theme-color);
  font-weight: 600;
  background: var(--theme-color-soft);
}

.side-cat.is-active .el-icon {
  color: var(--theme-color);
}

.side-help {
  display: flex;
  flex-direction: column;
}

.side-help__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  margin-bottom: 12px;
  color: var(--theme-color);
  font-size: 20px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.side-help h3 {
  margin: 0 0 8px;
  padding-left: 0;
  border-left: 0;
}

.side-help p {
  margin: 0 0 16px;
  color: var(--art-gray-500);
  font-size: 13px;
  line-height: 1.8;
}

.side-help__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 38px;
  color: var(--theme-color-contrast);
  font-size: 13px;
  font-weight: 600;
  background: var(--theme-color);
  border-radius: var(--radius-md);
  transition: background 0.16s ease;
}

.side-help__btn:hover {
  background: var(--theme-color-deep);
}

@media (max-width: 900px) {
  .notices-layout {
    grid-template-columns: 1fr;
  }

  .notices-side {
    position: static;
    flex-direction: row;
    flex-wrap: wrap;
  }

  .notices-side .side-card {
    flex: 1 1 260px;
  }
}

@media (max-width: 640px) {
  .notices-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .notices-search {
    max-width: 100%;
  }

  .notice-item {
    flex-direction: column;
  }

  .notice-item__thumb {
    width: 100%;
    height: 150px;
  }

  .skeleton--thumb {
    width: 100%;
    height: 150px;
  }

  .notices-side {
    flex-direction: column;
  }

  .notices-side .side-card {
    flex: 0 0 auto;
  }
}
</style>
