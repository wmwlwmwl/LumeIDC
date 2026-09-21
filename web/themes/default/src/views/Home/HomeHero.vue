<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowRight } from '@element-plus/icons-vue'
import { useSession } from '@/http/session'
import type { Category } from '@/api/store'
import PublicContainer from '@/components/public/PublicContainer.vue'

defineOptions({ name: 'HomeHero' })

const props = defineProps<{
  catalog: Category[]
  description?: string
}>()

const session = useSession()
const router = useRouter()
const activeIndex = ref(0)

const rails = computed(() => props.catalog.slice(0, 6))
const active = computed(() => rails.value[activeIndex.value] || null)

watch(
  () => props.catalog,
  () => {
    activeIndex.value = 0
  },
)

const features = [
  { icon: 'ri:flashlight-line', title: '分钟级开通', desc: '下单后自动交付，减少等待。' },
  { icon: 'ri:money-cny-circle-line', title: '透明计费', desc: '按需付费，账单清晰可查。' },
  { icon: 'ri:route-line', title: '稳定多线', desc: 'BGP 多线接入，连接可靠。' },
  { icon: 'ri:customer-service-2-line', title: '7×24 支持', desc: '随时响应你的服务需求。' },
]

function goActive() {
  const a = active.value
  if (a) router.push({ path: '/cart', query: { fid: String(a.id) } })
  else router.push('/cart')
}

const railRef = ref<HTMLElement | null>(null)
function onTabKey(e: KeyboardEvent, i: number) {
  const total = rails.value.length
  if (!total) return
  let next = i
  if (e.key === 'ArrowDown' || e.key === 'ArrowRight') next = (i + 1) % total
  else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') next = (i - 1 + total) % total
  else return
  e.preventDefault()
  activeIndex.value = next
  void nextTick(() => {
    railRef.value?.querySelectorAll<HTMLElement>('.hero-rail__item')[next]?.focus()
  })
}
</script>

<template>
  <section class="hero">
    <PublicContainer>
      <div class="art-card hero__panel">
        <div class="hero__stage">
          <aside ref="railRef" class="hero__rail" role="tablist" aria-label="产品分类">
            <button
              v-for="(c, i) in rails"
              :id="`hero-tab-${c.id}`"
              :key="c.id"
              type="button"
              role="tab"
              class="hero-rail__item"
              :class="{ 'is-active': i === activeIndex }"
              :aria-selected="i === activeIndex"
              :tabindex="i === activeIndex ? 0 : -1"
              aria-controls="hero-panel"
              @mouseenter="activeIndex = i"
              @click="activeIndex = i"
              @keydown="onTabKey($event, i)"
            >
              <span class="hero-rail__label">{{ c.name }}</span>
              <el-icon class="hero-rail__arrow"><ArrowRight /></el-icon>
            </button>
            <RouterLink v-if="!rails.length" to="/cart" class="hero-rail__item is-active">
              <span class="hero-rail__label">全部产品</span>
            </RouterLink>
          </aside>

          <div
            id="hero-panel"
            class="hero__body"
            role="tabpanel"
            :aria-labelledby="active ? `hero-tab-${active.id}` : undefined"
          >
            <h1 class="hero__title">{{ active?.name || '为增长而生的云基础设施' }}</h1>
            <p class="hero__desc">
              {{ props.description || '从灵活的云服务器到可扩展的网络资源，为你的业务提供稳定、安全且简单的基础设施能力。' }}
            </p>
            <div class="hero__actions">
              <button type="button" class="hero-cta hero-cta--primary" @click="goActive">
                浏览产品 <el-icon><ArrowRight /></el-icon>
              </button>
              <RouterLink v-if="!session.user" to="/register" class="hero-cta hero-cta--secondary">
                创建账户
              </RouterLink>
              <RouterLink v-else to="/services" class="hero-cta hero-cta--secondary">我的服务</RouterLink>
            </div>
          </div>
        </div>
      </div>
    </PublicContainer>

    <PublicContainer>
      <div class="hero-features">
        <article v-for="f in features" :key="f.title" class="hero-feature">
          <span class="hero-feature__icon"><ArtSvgIcon :icon="f.icon" /></span>
          <div class="hero-feature__copy">
            <strong class="hero-feature__title">{{ f.title }}</strong>
            <p class="hero-feature__desc">{{ f.desc }}</p>
          </div>
        </article>
      </div>
    </PublicContainer>
  </section>
</template>

<style scoped>
.hero {
  position: relative;
  padding: 24px 0 32px;
}

.hero__panel {
  padding: 36px 40px;
}

.hero__stage {
  display: grid;
  grid-template-columns: 224px minmax(0, 1fr);
  column-gap: 40px;
  align-items: center;
}

.hero__rail {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.hero-rail__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-height: 50px;
  padding: 12px 18px;
  text-align: left;
  color: var(--art-gray-700);
  font-size: 15px;
  font-weight: 500;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--custom-radius);
  cursor: pointer;
  transition: color 0.2s ease, background 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease,
    transform 0.2s ease;
}

.hero-rail__item:hover {
  color: var(--theme-color);
  border-color: color-mix(in srgb, var(--theme-color) 40%, var(--art-card-border));
  transform: translateX(2px);
}

.hero-rail__item.is-active {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
  border-color: var(--theme-color);
  box-shadow: 0 12px 26px color-mix(in srgb, var(--theme-color) 30%, transparent);
}

.hero-rail__label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.hero-rail__arrow {
  flex-shrink: 0;
  font-size: 12px;
  opacity: 0;
  transition: opacity 0.2s ease;
}

.hero-rail__item.is-active .hero-rail__arrow {
  opacity: 1;
}

.hero__body {
  min-width: 0;
}

.hero__title {
  margin: 0;
  color: var(--art-gray-900);
  font-size: clamp(30px, 4vw, 46px);
  font-weight: 700;
  line-height: 1.16;
  letter-spacing: -0.03em;
}

.hero__desc {
  max-width: 620px;
  margin: 20px 0 0;
  color: var(--art-gray-500);
  font-size: 15px;
  line-height: 1.9;
}

.hero__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  margin-top: 30px;
}

.hero-cta {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-width: 140px;
  height: 46px;
  padding: 0 26px;
  font-size: 15px;
  font-weight: 600;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: transform 0.2s ease, background 0.2s ease, box-shadow 0.2s ease;
}

.hero-cta--primary {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
  border: 0;
  box-shadow: 0 14px 28px color-mix(in srgb, var(--theme-color) 30%, transparent);
}

.hero-cta--primary:hover {
  background: var(--theme-color-deep);
  transform: translateY(-2px);
}

.hero-cta--secondary {
  color: var(--theme-color);
  background: var(--default-box-color);
  border: 1px solid color-mix(in srgb, var(--theme-color) 28%, transparent);
}

.hero-cta--secondary:hover {
  background: var(--theme-color-soft);
  transform: translateY(-2px);
}

.hero-features {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-top: 34px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.hero-feature {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 20px 22px;
  border-left: 1px solid var(--art-card-border);
}

.hero-feature:first-child {
  border-left: 0;
}

.hero-feature__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 38px;
  height: 38px;
  color: var(--theme-color);
  font-size: 18px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.hero-feature__copy {
  min-width: 0;
}

.hero-feature__title {
  display: block;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 600;
}

.hero-feature__desc {
  margin: 6px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.7;
}

@media (max-width: 960px) {
  .hero {
    padding: 40px 0 34px;
  }

  .hero__panel {
    padding: 24px 20px;
  }

  .hero__stage {
    grid-template-columns: 1fr;
    row-gap: 20px;
  }

  .hero__rail {
    flex-direction: row;
    overflow-x: auto;
    gap: 8px;
    padding-bottom: 4px;
  }

  .hero-rail__item {
    flex: 0 0 auto;
    min-height: 0;
    padding: 9px 16px;
    border-radius: 999px;
    font-size: 14px;
  }

  .hero-rail__arrow {
    display: none;
  }

  .hero-features {
    grid-template-columns: repeat(2, 1fr);
  }

  .hero-feature:nth-child(3) {
    border-left: 0;
  }

  .hero-feature:nth-child(n + 3) {
    border-top: 1px solid var(--art-card-border);
  }
}

@media (max-width: 560px) {
  .hero-features {
    grid-template-columns: 1fr;
  }

  .hero-feature {
    border-left: 0;
  }

  .hero-feature + .hero-feature {
    border-top: 1px solid var(--art-card-border);
  }
}
</style>
