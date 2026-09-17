<script setup lang="ts">
/**
 * ZJMF 实例信息卡：只读运行信息表（IP/端口/系统/带宽等）+ 刷新按钮。
 */
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { CopyDocument, InfoFilled } from '@element-plus/icons-vue'
import { refreshServiceHost, type DetailData } from '../../../api/user'
import { copyText } from '@/utils/clipboard'

const props = defineProps<{ data: DetailData; serviceId: number }>()
const emit = defineEmits<{ refresh: [] }>()

async function copy(value: string) {
  if (await copyText(value)) ElMessage.success('已复制')
  else ElMessage.error('复制失败，请手动复制')
}

const refreshingHost = ref(false)
async function refreshHost() {
  refreshingHost.value = true
  try {
    const res = await refreshServiceHost(props.serviceId)
    if (String(res.ok) === '1') { ElMessage.success('实例信息已刷新'); emit('refresh') }
    else ElMessage.error(res.msg || '刷新失败')
  } catch (e: unknown) { ElMessage.error((e as Error).message || '刷新失败') }
  finally { refreshingHost.value = false }
}
</script>

<template>
  <section class="zjmf-card art-card">
    <div class="zjmf-card__header">
      <div class="zjmf-card__title">
        <h2>实例信息</h2>
        <p>实例运行信息</p>
      </div>
      <el-button size="small" text type="primary" :loading="refreshingHost" @click="refreshHost">刷新信息</el-button>
    </div>

    <div v-if="data.host" class="zjmf-info-table">
      <div class="zjmf-info-row">
        <span class="zjmf-info-label">状态</span>
        <b class="zjmf-info-value">{{ data.host.status || data.svc.status_text }}</b>
      </div>
      <div v-if="data.host.username" class="zjmf-info-row">
        <span class="zjmf-info-label">用户名</span>
        <b class="zjmf-info-value zjmf-mono">{{ data.host.username }}</b>
      </div>
      <div v-if="data.host.password" class="zjmf-info-row">
        <span class="zjmf-info-label">密码</span>
        <b class="zjmf-info-cell">
          <span class="zjmf-info-value zjmf-mono">{{ data.host.password }}</span>
          <el-button class="zjmf-copy-btn" size="small" text @click="copy(data.host.password)">
            <el-icon :size="13"><CopyDocument /></el-icon>
            复制
          </el-button>
        </b>
      </div>
      <div v-if="data.host.ip" class="zjmf-info-row">
        <span class="zjmf-info-label">实例 IP</span>
        <b class="zjmf-info-value zjmf-mono">{{ data.host.ip }}</b>
      </div>
      <div v-for="ip in data.host.additional_ips || []" :key="ip" class="zjmf-info-row">
        <span class="zjmf-info-label">附加 IP</span>
        <b class="zjmf-info-value zjmf-mono">{{ ip }}</b>
      </div>
      <div v-if="data.host.port" class="zjmf-info-row">
        <span class="zjmf-info-label">端口</span>
        <b class="zjmf-info-value zjmf-mono">{{ data.host.port }}</b>
      </div>
      <div v-if="data.host.os" class="zjmf-info-row">
        <span class="zjmf-info-label">系统名称</span>
        <b class="zjmf-info-value">{{ data.host.os }}</b>
      </div>
      <div v-if="data.host.os_version" class="zjmf-info-row">
        <span class="zjmf-info-label">系统版本</span>
        <b class="zjmf-info-value">{{ data.host.os_version }}</b>
      </div>
      <div v-if="data.host.bw_limit" class="zjmf-info-row">
        <span class="zjmf-info-label">带宽</span>
        <b class="zjmf-info-value">
          {{ data.host.bw_limit }}{{ data.host.bw_usage ? `（已用 ${data.host.bw_usage}）` : '' }}
        </b>
      </div>
      <div v-if="data.host.datacenter" class="zjmf-info-row">
        <span class="zjmf-info-label">数据中心</span>
        <b class="zjmf-info-value">{{ data.host.datacenter }}</b>
      </div>
    </div>
    <div v-else class="zjmf-empty-hint">
      <el-icon size="16"><InfoFilled /></el-icon>
      <span>尚未获取实例信息，点击「刷新信息」重新获取。</span>
    </div>
  </section>
</template>

<style scoped>
/* 盒样式（背景/描边/圆角/阴影）交由全局 art-card 按 data-box-mode 接管。
   本卡在 .zjmf-primary-grid 里是 align-items:stretch 的拉伸项：高度由更高的
   「实例控制台」决定，所以要用 flex 列让信息表吃掉剩余高度，否则最后一行下面
   会堆一大块空白。 */
.zjmf-card {
  display: flex;
  flex-direction: column;
  padding: 20px 22px;
}
.zjmf-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--art-card-border);
}
.zjmf-card__title h2 {
  margin: 4px 0 0;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.3;
}
.zjmf-card__title p {
  margin: 4px 0 0;
  color: var(--art-gray-400);
  font-size: 11.5px;
}

/* --- 空实例提示 --- */
.zjmf-empty-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 14px 0 4px;
  padding: 12px 14px;
  color: var(--art-gray-500);
  font-size: 12px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: 8px;
}

/* --- 实例信息表格 --- */
.zjmf-info-table {
  display: grid;
  /* 吃掉卡片剩余高度，剩余空间只摊到行间距上（与 EasyPanel 侧思路一致）。
     注意别用 grid-auto-rows: 1fr：那会把每行都撑到最高那行的高度，
     本卡会顶得比左卡还高，空白反而转嫁给左卡。 */
  flex: 1 1 auto;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-content: space-between;
  column-gap: 28px;
}
.zjmf-info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 0;
  font-size: 12.5px;
  border-bottom: 1px dashed var(--art-card-border);
}
/* 末行只剩一个字段时（字段数为奇数）独占整行：否则右半格空着，行被拉高后更显眼 */
.zjmf-info-row:last-child:nth-child(odd) {
  grid-column: 1 / -1;
}
.zjmf-info-row:last-child {
  border-bottom: none;
}
.zjmf-info-label {
  flex: 0 0 auto;
  color: var(--art-gray-500);
  font-weight: 450;
}
.zjmf-info-value {
  /* 不再限制 65%：值是 flex 项且标签 flex:0 0 auto 不会缩，值本来就该用满剩余宽度。
     半宽列里 65% 会把 Windows-2022-Datacenter-cn 这种值提前挤成两行。 */
  max-width: 100%;
  color: var(--art-gray-800);
  font-weight: 600;
  text-align: right;
  /* 不要用 word-break: break-all：它会在任意字符处断行，把
     Windows-2022-Datacenter-cn 切成 Windows-2022-Da / tacent er-cn。
     overflow-wrap: anywhere 只在必要时断，优先落在连字符这类自然位置；
     长 IP / 长密码这类无分隔串照样能换行（它同样参与 min-content 计算）。 */
  overflow-wrap: anywhere;
}
/* 带操作按钮的值：值 + 按钮包成右对齐的一格，避免 3 个子元素走 space-between
   时值被推到行中间（密码行原来的问题）。 */
.zjmf-info-cell {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}
.zjmf-info-cell .zjmf-copy-btn {
  margin-left: 0;
}
.zjmf-mono {
  font-family: ui-monospace, SFMono-Regular, Consolas, 'Liberation Mono', monospace;
  font-size: 12px;
}
.zjmf-copy-btn {
  flex: 0 0 auto;
  margin-left: 4px;
  padding: 2px 6px;
  color: var(--theme-color-deep);
  font-size: 11px;
}
.zjmf-copy-btn:hover {
  color: var(--theme-color);
}

@media (max-width: 560px) {
  .zjmf-info-table {
    grid-template-columns: 1fr;
  }
}
</style>
