<script setup lang="ts">
/**
 * 顶部 mega-menu 面板：产品（两级目录联动）/ 公告 / 其他 三种内容。
 * 打开状态由父级 Header 的导航按钮驱动（active prop）；
 * 悬停保持与延迟关闭的计时器逻辑由父级管理，本组件只上报 enter/leave。
 */
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useSession } from '@/http/session'
import { fetchCatalog, fetchHome, type Category, type Announcement } from '@/api/store'
import PublicContainer from '@/components/public/PublicContainer.vue'

const props = defineProps<{ active: '' | 'products' | 'notices' | 'other' }>()
const emit = defineEmits<{ enter: []; leave: []; close: [] }>()

const router = useRouter()
const session = useSession()

const catalog = ref<Category[]>([])
const notices = ref<Announcement[]>([])
const activeType = ref<number | string>('')

const activeGroups = computed(() => {
  const current = catalog.value.find((c) => String(c.id) === String(activeType.value)) || catalog.value[0]
  return current?.children || []
})

async function ensureProducts() {
  if (catalog.value.length) return
  try {
    const data = await fetchCatalog('', '')
    catalog.value = data.catalog || []
    if (catalog.value[0]) activeType.value = catalog.value[0].id
  } catch {
    /* 菜单数据失败时保持空，不阻塞页面 */
  }
}
async function ensureNotices() {
  if (notices.value.length) return
  try {
    const data = await fetchHome()
    notices.value = (data.announcements || []).slice(0, 6)
  } catch {
    /* ignore */
  }
}

watch(
  () => props.active,
  (menu) => {
    if (menu === 'products') void ensureProducts()
    if (menu === 'notices') void ensureNotices()
  },
  { immediate: true },
)

function goGroup(fid: number, gid: number) {
  emit('close')
  router.push({ path: '/cart', query: { fid: String(fid), gid: String(gid) } })
}
</script>

<template>
  <Transition name="mega">
    <div
      v-if="active"
      class="mega"
      @mouseenter="emit('enter')"
      @mouseleave="emit('leave')"
    >
      <PublicContainer class="mega__inner">
        <template v-if="active === 'products'">
          <div class="mega__types">
            <button
              v-for="c in catalog"
              :key="c.id"
              type="button"
              class="mega-type"
              :class="{ active: String(activeType) === String(c.id) }"
              @mouseenter="activeType = c.id"
            >
              <span>{{ c.name }}</span>
              <em>{{ c.children?.length || 0 }}</em>
            </button>
            <RouterLink to="/cart" class="mega-more">查看全部产品 →</RouterLink>
          </div>
          <div class="mega__groups">
            <button
              v-for="g in activeGroups"
              :key="g.id"
              type="button"
              class="mega-group"
              @click="goGroup((catalog.find((c) => String(c.id) === String(activeType)) || catalog[0])?.id, g.id)"
            >
              <strong>{{ g.name }}</strong>
              <span>{{ g.description || '查看该类目产品' }}</span>
            </button>
            <div v-if="!activeGroups.length" class="mega-empty">暂无二级分类</div>
          </div>
        </template>

        <template v-else-if="active === 'notices'">
          <div class="mega__types mega__types--text">
            <div class="mega-head">站点公告</div>
            <p class="mega-sub">产品更新、活动与维护通知</p>
            <RouterLink to="/notices" class="mega-more">查看全部公告 →</RouterLink>
          </div>
          <div class="mega__groups">
            <RouterLink v-for="n in notices" :key="n.id" to="/notices" class="mega-group">
              <strong>{{ n.title }}</strong>
              <span>{{ n.created_at }}</span>
            </RouterLink>
            <div v-if="!notices.length" class="mega-empty">暂无公告</div>
          </div>
        </template>

        <template v-else>
          <div class="mega__types mega__types--text">
            <div class="mega-head">更多</div>
            <p class="mega-sub">账户、服务与联系方式</p>
          </div>
          <div class="mega__groups">
            <RouterLink to="/user" class="mega-group"><strong>账户中心</strong><span>余额、账单与资料</span></RouterLink>
            <RouterLink to="/services" class="mega-group"><strong>我的服务</strong><span>管理已开通实例</span></RouterLink>
            <RouterLink to="/user/verification" class="mega-group"><strong>实名认证</strong><span>完成实名后购买</span></RouterLink>
            <a v-if="session.site.email" :href="`mailto:${session.site.email}`" class="mega-group"><strong>联系我们</strong><span>{{ session.site.email }}</span></a>
          </div>
        </template>
      </PublicContainer>
    </div>
  </Transition>
</template>

<style scoped>
/* mega menu（绝对定位于 header 下方） */
.mega {
  position: absolute;
  left: 0;
  right: 0;
  top: 64px;
  background: var(--default-box-color);
  border-bottom: 1px solid var(--art-card-border);
  box-shadow: 0 16px 40px rgba(15, 23, 42, 0.08);
}

.mega__inner {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr);
  min-height: 280px;
  padding-top: 8px;
  padding-bottom: 8px;
}

.mega__types {
  display: flex;
  flex-direction: column;
  padding: 12px 12px 12px 0;
  border-right: 1px solid var(--art-card-border);
}

.mega__types--text {
  padding-left: 0;
}

.mega-type {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 10px 16px;
  color: var(--art-gray-600);
  font-size: 13px;
  font-weight: 500;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: 8px;
  cursor: pointer;
  transition: color 0.14s ease, background 0.14s ease;
}

.mega-type:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
}

.mega-type.active {
  color: var(--theme-color);
  font-weight: 600;
  background: var(--theme-color-soft);
}

.mega-type em {
  color: var(--art-gray-400);
  font-size: 11px;
  font-style: normal;
}

.mega-head {
  padding: 10px 16px 2px;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 600;
}

.mega-sub {
  margin: 4px 0 0;
  padding: 0 16px;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.6;
}

.mega-more {
  padding: 12px 16px 0;
  margin-top: auto;
  color: var(--theme-color);
  font-size: 12px;
  font-weight: 600;
}

.mega__groups {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
  align-content: start;
  padding: 14px 16px 14px 20px;
}

.mega-group {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 13px 15px;
  text-align: left;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.14s ease, border-color 0.14s ease, transform 0.14s ease;
}

.mega-group:hover {
  background: var(--theme-color-soft);
  border-color: color-mix(in srgb, var(--theme-color) 34%, var(--art-card-border));
  transform: translateY(-1px);
}

.mega-group strong {
  color: var(--art-gray-800);
  font-size: 13px;
  font-weight: 600;
}

.mega-group:hover strong {
  color: var(--theme-color);
}

.mega-group span {
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mega-empty {
  grid-column: 1 / -1;
  padding: 40px 20px;
  color: var(--art-gray-400);
  font-size: 13px;
  text-align: center;
}

.mega-enter-active,
.mega-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.mega-enter-from,
.mega-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}

@media (max-width: 960px) {
  .mega {
    display: none;
  }
}
</style>
