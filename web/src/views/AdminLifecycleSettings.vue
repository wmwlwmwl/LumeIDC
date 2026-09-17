<script setup lang="ts">
import { useAdminSettings } from '../admin/useSettings'

const { loading, saving, cfg, load, save } = useAdminSettings()

// 后端以字符串返回配置值，数字输入框需要 number
function num(key: string, fallback: number): number {
  const n = Number(cfg[key])
  return Number.isFinite(n) ? n : fallback
}
function setNum(key: string, v: number | undefined) {
  cfg[key] = String(v ?? 0)
}

function saveLifecycle() {
  save('lifecycle', {
    service_suspend_after_days: String(num('service_suspend_after_days', 0)),
    service_terminate_after_days: String(num('service_terminate_after_days', 3)),
    service_expire_warn_days: String(num('service_expire_warn_days', 3)),
  })
}

load()
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>服务生命周期</h4>
            <p>到期后何时停机、何时标记删除，以及提前多久发送续费提醒。</p>
          </div>
        </div>
      </template>

      <div class="admin-form-grid">
        <el-form-item label="到期后停机（天）">
          <el-input-number
            :model-value="num('service_suspend_after_days', 0)"
            :min="0"
            :max="365"
            @update:model-value="(v: number | undefined) => setNum('service_suspend_after_days', v)"
          />
        </el-form-item>
        <el-form-item label="到期后删除（天）">
          <el-input-number
            :model-value="num('service_terminate_after_days', 3)"
            :min="1"
            :max="3650"
            @update:model-value="(v: number | undefined) => setNum('service_terminate_after_days', v)"
          />
        </el-form-item>
        <el-form-item label="到期前提醒（天）">
          <el-input-number
            :model-value="num('service_expire_warn_days', 3)"
            :min="1"
            :max="365"
            @update:model-value="(v: number | undefined) => setNum('service_expire_warn_days', v)"
          />
        </el-form-item>
      </div>

      <p class="mb-3 text-xs text-g-500">
        上游（如魔方财务）通常在到期 1~7 天内就真实销毁实例。本地删除阈值若远大于上游，会出现「机器已被上游释放、本地仍显示可续费」的服务，用户续费必然失败。因此默认取 3 天；上游确实同步了已删除状态时，本地会在 30 秒内跟随，不会等到这里。
      </p>
      <p class="mb-3 text-xs text-g-500">删除天数不能小于停机天数。</p>

      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'lifecycle'" @click="saveLifecycle">
          保存生命周期
        </el-button>
      </div>
    </ElCard>
  </div>
</template>
