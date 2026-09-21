<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight } from '@element-plus/icons-vue'
import type { ProductLite } from '@/api/store'
import PublicContainer from '@/components/public/PublicContainer.vue'
import PublicSectionHeader from '@/components/public/PublicSectionHeader.vue'
import PublicProductCard from '@/components/public/PublicProductCard.vue'

defineOptions({ name: 'HomeProductTabs' })

const props = defineProps<{ products: ProductLite[] }>()

const list = computed(() => props.products.slice(0, 8))
</script>

<template>
  <section class="products">
    <PublicContainer>
      <PublicSectionHeader
        title="安全、稳定、可信赖的产品与服务"
        subtitle="按业务需求选择合适的云基础设施资源。"
      >
        <template #extra>
          <RouterLink to="/cart" class="products__more">
            查看全部产品 <el-icon><ArrowRight /></el-icon>
          </RouterLink>
        </template>
      </PublicSectionHeader>

      <div v-if="list.length" class="products__grid">
        <PublicProductCard v-for="p in list" :key="p.id" :product="p" />
      </div>
      <div v-else class="products__empty">暂无可展示的产品，请稍后再来。</div>
    </PublicContainer>
  </section>
</template>

<style scoped>
.products {
  padding: 40px 0 12px;
}

.products__more {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  border-radius: var(--radius-sm);
}

.products__more .el-icon {
  transition: transform 0.16s ease;
}

.products__more:hover .el-icon {
  transform: translateX(3px);
}

.products__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(270px, 1fr));
  gap: 16px;
}

.products__empty {
  padding: 48px 0;
  color: var(--art-gray-500);
  font-size: 14px;
  text-align: center;
}
</style>
