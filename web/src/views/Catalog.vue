<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Search, ArrowDown } from '@element-plus/icons-vue'
import { fetchCatalog, type Category, type CategoryChild, type ProductLite } from '../api/store'
import PublicContainer from '@/components/public/PublicContainer.vue'
import PublicProductCard from '@/components/public/PublicProductCard.vue'

const route = useRoute()
const router = useRouter()
const loading = ref(false)
const loadError = ref(false)
const catalog = ref<Category[]>([])
const products = ref<ProductLite[]>([])
const fid = ref('')
const gid = ref('')
const search = ref('')
const catDesc = ref('')
const catOpen = ref(false)
const openGroup = ref('')

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return products.value
  return products.value.filter((p) => p.name.toLowerCase().includes(q) || (p.desc || '').toLowerCase().includes(q))
})
const currentName = computed(() => catalog.value.find((c) => String(c.id) === fid.value)?.name || '产品目录')

function toggleGroup(id: number | string) {
  const key = String(id)
  openGroup.value = openGroup.value === key ? '' : key
}

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const qFid = String(route.query.fid ?? '')
    const qGid = String(route.query.gid ?? '')
    // 只在带二级分类(gid)时才直接请求；否则后端会对 fid-only 发 303 重定向，
    // 浏览器跟随后拿到的是 SPA HTML 而非 JSON。无选择时先取分类列表，再由前端
    // 选中第一个「含二级分类」的一级分类及其第一个二级分类（不改 URL、不跳转）。
    let data = qGid ? await fetchCatalog(qFid, qGid) : await fetchCatalog('', '')
    if (!data.gid && data.catalog.length) {
      const byFid = qFid ? data.catalog.filter((c) => String(c.id) === qFid) : []
      const hasChildren = (c: Category) => (c.children?.length ?? 0) > 0
      const target = byFid.find(hasChildren) || data.catalog.find(hasChildren)
      if (target?.children?.[0]) {
        data = await fetchCatalog(target.id, target.children[0].id)
      } else if (data.catalog[0]) {
        data = await fetchCatalog(data.catalog[0].id, '')
      }
    }
    catalog.value = data.catalog
    products.value = data.products
    fid.value = data.fid
    gid.value = data.gid
    catDesc.value = ''
    for (const c of catalog.value) {
      if (String(c.id) !== data.fid) continue
      for (const sub of c.children || []) if (String(sub.id) === data.gid && sub.description) catDesc.value = sub.description
    }
    openGroup.value = fid.value || ''
  } catch (err: unknown) {
    loadError.value = true
    ElMessage.error((err as Error).message || '产品加载失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(() => [route.query.fid, route.query.gid], load)

function selectCategory(c: Category, sub: CategoryChild) {
  catOpen.value = false
  router.push({ path: '/cart', query: { fid: String(c.id), gid: String(sub.id) } })
}
function selectTop(c: Category) {
  catOpen.value = false
  router.push({ path: '/cart', query: { fid: String(c.id) } })
}
</script>

<template>
  <PublicContainer>
    <button class="catalog-mobile-toggle" type="button" @click="catOpen = !catOpen">
      <span><small>当前分类</small><strong>{{ currentName }}</strong></span>
      <span>{{ catOpen ? '收起' : '切换分类' }}</span>
    </button>

    <div class="catalog-layout">
      <aside class="art-card catalog-sidebar" :class="{ 'is-open': catOpen }">
        <div class="catalog-search">
          <el-icon><Search /></el-icon>
          <input
            v-model="search"
            type="search"
            aria-label="搜索产品名称"
            placeholder="搜索产品名称"
          />
        </div>

        <div class="catalog-sidebar__label">产品分类</div>
        <nav class="cat-nav">
          <div
            v-for="c in catalog"
            :key="c.id"
            class="cat-group"
            :class="{ 'is-open': openGroup === String(c.id) }"
          >
            <button
              type="button"
              class="cat-group__head"
              :aria-expanded="c.children?.length ? openGroup === String(c.id) : undefined"
              @click="c.children?.length ? toggleGroup(c.id) : selectTop(c)"
            >
              <span class="cat-group__name">{{ c.name }}</span>
              <span v-if="c.children?.length" class="cat-group__meta">{{ c.children.length }}</span>
              <el-icon v-if="c.children?.length" class="cat-group__arrow"><ArrowDown /></el-icon>
            </button>
            <ul v-if="c.children?.length" v-show="openGroup === String(c.id)" class="cat-group__list">
              <li v-for="sub in c.children" :key="sub.id" class="cat-item">
                <button
                  type="button"
                  class="cat-item__btn"
                  :class="{ 'is-active': String(sub.id) === gid }"
                  :aria-current="String(sub.id) === gid ? 'true' : undefined"
                  @click="selectCategory(c, sub)"
                >
                  <span class="cat-item__name">{{ sub.name }}</span>
                </button>
              </li>
            </ul>
          </div>
        </nav>
      </aside>

      <main class="catalog-main">
        <div class="art-card catalog-main__head">
          <div>
            <h2>{{ currentName }}</h2>
            <p>{{ catDesc || '从以下产品中选择配置，立即开始部署。' }}</p>
          </div>
          <span class="catalog-main__result">{{ filtered.length }} 个结果</span>
        </div>

        <div v-if="loading" class="art-card catalog-skeleton" aria-hidden="true">
          <el-skeleton :rows="6" animated />
        </div>
        <div v-else-if="loadError" class="art-card catalog-state" role="alert">
          <p>产品加载失败，请检查网络后重试。</p>
          <el-button size="small" @click="load">重新加载</el-button>
        </div>
        <div v-else-if="filtered.length" class="catalog-product-grid">
          <PublicProductCard v-for="p in filtered" :key="p.id" :product="p" />
        </div>
        <el-empty v-else :description="search ? '没有匹配的产品' : '该分类暂无上架产品'" />
      </main>
    </div>
  </PublicContainer>
</template>

<style scoped>
.catalog-layout {
  display: grid;
  grid-template-columns: 258px minmax(0, 1fr);
  gap: 18px;
  align-items: start;
}

.catalog-sidebar {
  padding: 14px;
  position: sticky;
  top: 80px;
}

.catalog-search {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 40px;
  padding: 0 11px;
  color: var(--art-gray-500);
  background: var(--art-gray-100);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.catalog-search:focus-within {
  border-color: var(--theme-color);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--theme-color) 12%, transparent);
}

.catalog-search input {
  width: 100%;
  color: var(--art-gray-700);
  font: inherit;
  font-size: 13px;
  outline: 0;
  background: transparent;
  border: 0;
}

.catalog-search input::placeholder {
  color: var(--art-gray-500);
}

.catalog-sidebar__label {
  margin: 20px 9px 7px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 600;
}

.cat-nav {
  width: 100%;
}

.cat-group + .cat-group {
  margin-top: 2px;
}

.cat-group__head {
  display: flex;
  align-items: center;
  width: 100%;
  min-height: 46px;
  padding: 0 12px;
  color: var(--art-gray-800);
  font-size: 15px;
  font-weight: 600;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease;
}

.cat-group__head:hover {
  color: var(--theme-color);
  background: var(--art-gray-100);
}

.cat-group.is-open .cat-group__head {
  color: var(--theme-color);
}

.cat-group__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cat-group__meta {
  margin-left: 6px;
  color: var(--art-gray-400);
  font-size: 12px;
  font-weight: 400;
}

.cat-group__arrow {
  margin-left: auto;
  flex-shrink: 0;
  color: var(--art-gray-400);
  font-size: 13px;
  transition: transform 0.2s ease;
}

.cat-group.is-open .cat-group__arrow {
  transform: rotate(180deg);
}

.cat-group__list {
  margin: 2px 0 0;
  padding: 0;
  list-style: none;
}

.cat-item {
  border-bottom: 1px solid var(--art-card-border);
}

.cat-group__list .cat-item:last-child {
  border-bottom: 0;
}

.cat-item__btn {
  position: relative;
  display: flex;
  align-items: center;
  width: 100%;
  min-height: 44px;
  padding: 0 12px 0 24px;
  color: var(--art-gray-600);
  font-size: 14px;
  text-align: left;
  background: transparent;
  border: 0;
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease;
}

.cat-item__btn:hover {
  color: var(--theme-color);
  background: var(--art-gray-100);
}

.cat-item__btn.is-active {
  color: var(--theme-color);
  font-weight: 600;
  background: var(--theme-color-soft);
}

.cat-item__btn.is-active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3px;
  background: var(--theme-color);
}

.cat-item__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.catalog-main {
  min-width: 0;
}

.catalog-main__head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 15px;
  margin-bottom: 14px;
  padding: 20px 22px;
}

.catalog-main__head h2 {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 19px;
  font-weight: 600;
}

.catalog-main__head p {
  margin: 6px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.catalog-main__result {
  padding: 5px 10px;
  flex-shrink: 0;
  color: var(--art-gray-600);
  font-size: 12px;
  background: var(--art-gray-100);
  border-radius: var(--radius-sm);
}

.catalog-skeleton,
.catalog-state {
  padding: 22px;
}

.catalog-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 44px 22px;
  text-align: center;
}

.catalog-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

.catalog-product-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.catalog-mobile-toggle {
  display: none;
}

@media (max-width: 900px) {
  .catalog-sidebar {
    position: static;
  }
}

@media (max-width: 800px) {
  .catalog-layout {
    display: block;
  }

  .catalog-mobile-toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    margin-bottom: 12px;
    padding: 11px 13px;
    text-align: left;
    background: var(--default-box-color);
    border: 1px solid var(--art-card-border);
    border-radius: var(--radius-md);
    cursor: pointer;
  }

  .catalog-mobile-toggle span:first-child {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .catalog-mobile-toggle small {
    color: var(--art-gray-500);
    font-size: 11px;
  }

  .catalog-mobile-toggle strong {
    color: var(--art-gray-700);
    font-size: 13px;
  }

  .catalog-mobile-toggle span:last-child {
    color: var(--art-gray-500);
    font-size: 12px;
  }

  .catalog-sidebar {
    display: none;
    margin-bottom: 13px;
  }

  .catalog-sidebar.is-open {
    display: block;
  }

  .catalog-product-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 520px) {
  .catalog-product-grid {
    grid-template-columns: 1fr;
  }

  .catalog-main__head {
    padding: 17px;
  }
}
</style>
