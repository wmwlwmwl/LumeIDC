<script setup lang="ts">
import { ArrowRight } from '@element-plus/icons-vue'
import type { Announcement } from '@/api/store'
import { formatDate } from '@/utils/format'
import PublicContainer from '@/components/public/PublicContainer.vue'
import PublicSectionHeader from '@/components/public/PublicSectionHeader.vue'

defineOptions({ name: 'HomeNews' })

const props = defineProps<{ notices: Announcement[] }>()

function excerpt(n: Announcement): string {
  return n.summary || n.content || ''
}
</script>

<template>
  <section class="news" v-if="props.notices.length">
    <PublicContainer>
      <PublicSectionHeader title="最新公告" subtitle="产品更新、活动与维护通知。">
        <template #extra>
          <RouterLink to="/notices" class="news__more">
            查看全部 <el-icon><ArrowRight /></el-icon>
          </RouterLink>
        </template>
      </PublicSectionHeader>

      <div class="news__list">
        <RouterLink
          v-for="n in props.notices.slice(0, 6)"
          :key="n.id"
          :to="`/notices/${n.id}`"
          class="art-card ncard"
        >
          <div class="ncard__body">
            <div class="ncard__top">
              <span v-if="n.pinned" class="ncard__pin">置顶</span>
              <span v-if="n.category" class="ncard__cat">{{ n.category }}</span>
              <time :datetime="n.created_at">{{ formatDate(n.created_at) }}</time>
            </div>
            <strong>{{ n.title }}</strong>
            <p v-if="excerpt(n)">{{ excerpt(n) }}</p>
          </div>
          <el-icon class="ncard__arrow"><ArrowRight /></el-icon>
        </RouterLink>
      </div>
    </PublicContainer>
  </section>
</template>

<style scoped>
.news {
  padding: 44px 0 8px;
}

.news__more {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  border-radius: var(--radius-sm);
}

.news__list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ncard {
  display: flex;
  align-items: center;
  gap: 18px;
  padding: 18px 22px;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.ncard:hover {
  transform: translateY(-2px);
  border-color: color-mix(in srgb, var(--theme-color) 32%, var(--art-card-border));
  box-shadow: 0 14px 30px color-mix(in srgb, var(--art-gray-900) 8%, transparent);
}

.ncard__body {
  flex: 1;
  min-width: 0;
}

.ncard__top {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.ncard__top time {
  margin-left: auto;
  flex-shrink: 0;
}

.ncard__pin {
  padding: 2px 8px;
  flex-shrink: 0;
  color: var(--el-color-danger);
  font-size: 11px;
  font-weight: 600;
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-sm);
}

.ncard__cat {
  padding: 2px 8px;
  flex-shrink: 0;
  color: var(--theme-color);
  font-size: 11px;
  font-weight: 600;
  background: var(--theme-color-soft);
  border-radius: var(--radius-sm);
}

.ncard__body strong {
  display: block;
  margin-top: 8px;
  overflow: hidden;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ncard:hover .ncard__body strong {
  color: var(--theme-color);
}

.ncard__body p {
  margin: 6px 0 0;
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 13px;
  line-height: 1.7;
  display: -webkit-box;
  -webkit-line-clamp: 1;
  line-clamp: 1;
  -webkit-box-orient: vertical;
}

.ncard__arrow {
  flex-shrink: 0;
  color: var(--art-gray-300);
  font-size: 16px;
  transition: transform 0.16s ease, color 0.16s ease;
}

.ncard:hover .ncard__arrow {
  color: var(--theme-color);
  transform: translateX(3px);
}
</style>
