<script setup lang="ts">
import PublicContainer from '@/components/public/PublicContainer.vue'
import PublicSectionHeader from '@/components/public/PublicSectionHeader.vue'

defineOptions({ name: 'HomeSolutions' })

const solutions = [
  { icon: 'ri:cpu-line', title: '弹性计算', desc: '按需调整 CPU、内存与磁盘，随业务弹性伸缩。' },
  { icon: 'ri:global-line', title: '稳定网络', desc: 'BGP 多线接入与带宽保障，连接稳定低时延。' },
  { icon: 'ri:shield-check-line', title: '安全防护', desc: '安全组、快照备份与权限分级，保障数据安全。' },
  { icon: 'ri:customer-service-2-line', title: '专业支持', desc: '7×24 工单与一对一支持，护航业务稳定运行。' },
]
</script>

<template>
  <section class="solutions">
    <PublicContainer>
      <PublicSectionHeader
        title="面向多种业务场景"
        subtitle="计算、网络、安全与服务的统一基础设施能力。"
      />
      <div class="solutions__grid">
        <article v-for="s in solutions" :key="s.title" class="scard">
          <span class="scard__icon"><ArtSvgIcon :icon="s.icon" /></span>
          <strong>{{ s.title }}</strong>
          <p>{{ s.desc }}</p>
        </article>
      </div>
    </PublicContainer>
  </section>
</template>

<style scoped>
.solutions {
  margin-top: 40px;
  padding: 52px 0;
  background: linear-gradient(
    180deg,
    color-mix(in srgb, var(--theme-color) 9%, var(--default-box-color)),
    var(--default-box-color) 72%
  );
  border-top: 1px solid var(--art-card-border);
  border-bottom: 1px solid var(--art-card-border);
}

.solutions__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;
}

.scard {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 24px 22px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-lg);
  transition: border-color 0.2s ease, background 0.2s ease, transform 0.2s ease;
}

.scard:hover {
  transform: translateY(-3px);
  background: var(--theme-color-soft);
  border-color: color-mix(in srgb, var(--theme-color) 34%, var(--art-card-border));
}

.scard__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  height: 46px;
  color: var(--theme-color);
  font-size: 23px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  transition: background 0.2s ease, color 0.2s ease;
}

.scard:hover .scard__icon {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
  border-color: var(--theme-color);
}

.scard strong {
  color: var(--art-gray-900);
  font-size: 16px;
  font-weight: 600;
}

.scard p {
  margin: 0;
  color: var(--art-gray-500);
  font-size: 13px;
  line-height: 1.75;
}

@media (max-width: 900px) {
  .solutions__grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .solutions__grid {
    grid-template-columns: 1fr;
  }
}
</style>
