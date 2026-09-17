<script setup lang="ts">
import { ref, computed, useSlots } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { InfoFilled } from '@element-plus/icons-vue'
import { consoleAction, refreshServiceHost, type DetailData } from '../../api/user'
import { copyText } from '@/utils/clipboard'

const props = defineProps<{ data: DetailData }>()
const slots = useSlots()
const emit = defineEmits<{ refresh: [] }>()

const busy = ref(false)
const refreshingHost = ref(false)
const activeView = ref('overview')

const password = computed(() => props.data.host?.password || '')
// 上游站点状态为「运行中 / 已关闭」两态（见 easypanel.HostOverview）
const hostOnline = computed(() => props.data.host?.status === '运行中')

async function refreshHost() {
  refreshingHost.value = true
  try {
    const res = await refreshServiceHost(props.data.svc.id)
    if (String(res.ok) === '1') {
      ElMessage.success('实例信息已刷新')
      emit('refresh')
    } else {
      ElMessage.error(res.msg || '刷新失败')
    }
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '刷新失败')
  } finally {
    refreshingHost.value = false
  }
}

// 密码必须能复制到别处（面板 / FTP 都要用），保留复制按钮；
// 面板地址不设复制按钮：链接本身可点，右键「复制链接地址」即可拿到完整 href，
// 单独放按钮反而挤占宽度、把地址截得更短。
async function copy() {
  if (await copyText(password.value)) ElMessage.success('已复制')
  else ElMessage.error('复制失败，请手动复制')
}

async function resetPassword() {
  const value = await ElMessageBox.prompt('留空自动生成', '重置站点密码', {
    confirmButtonText: '重置',
    cancelButtonText: '取消',
    inputValue: '',
  }).catch(() => null)
  if (value === null) return
  busy.value = true
  try {
    const res = await consoleAction(props.data.svc.id, { do: 'crack_pass', password: value.value })
    if (String(res.ok) === '1') ElMessage.success(res.msg || '密码已重置')
    else ElMessage.error(res.msg || '重置失败')
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '重置失败')
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <div class="easypanel-shell art-card">
    <nav class="easypanel-tabs" aria-label="实例功能">
      <button type="button" :class="{ 'is-active': activeView === 'overview' }" @click="activeView = 'overview'">概要</button>
      <button v-if="slots.invoices" type="button" :class="{ 'is-active': activeView === 'invoices' }" @click="activeView = 'invoices'">账单记录</button>
    </nav>

    <div v-if="activeView === 'overview'" class="easypanel-body">
      <section v-if="!data.host" class="easypanel-card art-card">
        <div class="easypanel-card__header">
          <div class="easypanel-card__title">
            <h2>站点信息</h2>
            <p>站点账号与资源配置</p>
          </div>
          <el-button size="small" text type="primary" :loading="refreshingHost" @click="refreshHost">刷新信息</el-button>
        </div>
        <div class="easypanel-empty-hint">
          <el-icon size="16"><InfoFilled /></el-icon>
          <span>尚未获取站点信息，点击「刷新信息」重新获取。</span>
        </div>
      </section>

      <div v-else class="easypanel-grid">
        <section class="easypanel-card art-card">
          <div class="easypanel-card__header">
            <div class="easypanel-card__title">
              <h2>账户信息</h2>
              <p>站点登录凭据</p>
            </div>
            <el-tag v-if="data.host.status" size="small" :type="hostOnline ? 'success' : 'info'">{{ data.host.status }}</el-tag>
          </div>
          <div class="easypanel-info-list">
            <div class="easypanel-info-row">
              <span>站点账号</span>
              <b class="easypanel-info-cell" :title="data.host.username || ''">{{ data.host.username || '-' }}</b>
            </div>
            <div class="easypanel-info-row">
              <span>站点密码</span>
              <b class="easypanel-info-cell">
                <span class="easypanel-info-cell__text" :title="password">{{ password || '-' }}</span>
                <el-button v-if="password" size="small" text @click="copy">复制</el-button>
              </b>
            </div>
            <div v-if="data.host.panel_url" class="easypanel-info-row easypanel-info-row--wide">
              <span>面板地址</span>
              <b class="easypanel-info-cell easypanel-info-cell--wide">
                <a
                  class="easypanel-info-cell__link easypanel-panel-link"
                  :href="data.host.panel_url"
                  target="_blank"
                  rel="noopener"
                  :title="data.host.panel_url"
                >{{ data.host.panel_url }}</a>
              </b>
            </div>
          </div>
          <div class="easypanel-card__footer">
            <span class="easypanel-footer-label">站点面板</span>
            <div class="easypanel-footer-actions">
              <el-button size="small" plain :loading="busy" @click="resetPassword">重置站点密码</el-button>
              <!-- 提交地址与展示地址必须分开：展示地址是面板首页（可手动登录），
                   提交地址是 a=login 登录入口；用展示地址提交会被当成空凭证登录而报错。 -->
              <form
                v-if="data.host.panel_login_url || data.host.panel_url"
                :action="data.host.panel_login_url || data.host.panel_url"
                method="post"
                target="_blank"
                rel="noopener"
              >
                <input type="hidden" name="username" :value="data.host.username || ''">
                <input type="hidden" name="passwd" :value="password">
                <el-button native-type="submit" type="primary" size="small">登录主机面板</el-button>
              </form>
            </div>
          </div>
        </section>

        <section class="easypanel-card art-card">
          <div class="easypanel-card__header">
            <div class="easypanel-card__title">
              <h2>空间与数据库</h2>
              <p>站点资源配置</p>
            </div>
            <el-button size="small" text type="primary" :loading="refreshingHost" @click="refreshHost">刷新信息</el-button>
          </div>
          <div class="easypanel-info-list">
            <div v-if="data.host.os">
              <span>运行环境</span>
              <b>{{ (data.host.os || '').toUpperCase() }}</b>
            </div>
            <div>
              <span>网页空间</span>
              <b>{{ data.host.web_quota || '-' }}</b>
            </div>
            <div v-if="data.host.db_name">
              <span>数据库</span>
              <b>{{ data.host.db_name }}{{ data.host.db_quota ? `（${data.host.db_quota}）` : '' }} · 已用 {{ data.host.db_used || '-' }}</b>
            </div>
            <div>
              <span>FTP</span>
              <b>{{ data.host.ftp ? '已开启' : '未开启' }}</b>
            </div>
            <div v-if="data.host.domain">
              <span>可绑域名</span>
              <b>{{ data.host.domain }}</b>
            </div>
            <div v-if="data.host.flow_limit">
              <span>流量限制</span>
              <b>{{ data.host.flow_limit }}</b>
            </div>
            <div v-if="data.host.speed_limit">
              <span>速度限制</span>
              <b>{{ data.host.speed_limit }}</b>
            </div>
            <div v-if="data.host.create_time">
              <span>创建时间</span>
              <b>{{ data.host.create_time }}</b>
            </div>
          </div>
        </section>
      </div>
    </div>
    <section v-if="activeView === 'invoices'" class="easypanel-invoices">
      <slot name="invoices" />
    </section>
  </div>
</template>
<style scoped>
.easypanel-shell {
  margin-top: 16px;
  overflow: hidden;
}
.easypanel-tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 51px;
  padding: 8px 12px;
  background: var(--art-gray-50);
  border-bottom: 1px solid var(--art-card-border);
  overflow-x: auto;
}
.easypanel-tabs button {
  flex: 0 0 auto;
  height: 34px;
  padding: 0 16px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 500;
  background: transparent;
  border: 0;
  border-radius: 7px;
  cursor: pointer;
  transition: color 0.2s ease, background-color 0.2s ease, border-color 0.2s ease;
}
.easypanel-tabs button:hover {
  color: var(--theme-color-deep);
  background: var(--art-hover-color);
}
.easypanel-tabs button.is-active {
  color: var(--theme-color-deep);
  font-weight: 600;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
}
.easypanel-body {
  padding: 16px;
}
/* 账单视图：插槽内容在 ServiceDetail.vue 挂 art-card 成浮卡，与内容区一致留白 */
.easypanel-invoices {
  margin: 16px;
}
.easypanel-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr);
  gap: 16px;
  align-items: stretch;
}
/* 盒样式（背景/描边/圆角/阴影）交由全局 art-card 按 data-box-mode 接管 */
.easypanel-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 20px 22px;
}
.easypanel-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--art-card-border);
}
.easypanel-card__title h2 {
  margin: 4px 0 0;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.3;
}
.easypanel-card__title p {
  margin: 4px 0 0;
  color: var(--art-gray-400);
  font-size: 11.5px;
}
/* 信息列表吃掉卡片的剩余高度：两卡在 grid 里 align-items:stretch 等高，
   账户卡只有 3 行、右卡最多 8 行，若不接管剩余高度，空白会整块堆在页脚上方。 */
.easypanel-info-list {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
}
.easypanel-info-list > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 0;
  font-size: 12.5px;
  border-bottom: 1px dashed var(--art-card-border);
}
.easypanel-info-list > div:last-child {
  border-bottom: 0;
}
.easypanel-info-list span {
  flex: 0 0 auto;
  color: var(--art-gray-500);
}
.easypanel-info-list b {
  max-width: 65%;
  color: var(--art-gray-800);
  font-weight: 600;
  text-align: right;
  overflow-wrap: anywhere;
  word-break: break-all;
}
.easypanel-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: auto;
  padding-top: 14px;
  border-top: 1px solid var(--art-card-border);
}
.easypanel-footer-label {
  color: var(--art-gray-400);
  font-size: 12px;
}
.easypanel-footer-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.easypanel-footer-actions form {
  margin: 0;
}
.easypanel-empty-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 14px 0 0;
  padding: 12px 14px;
  color: var(--art-gray-500);
  font-size: 12px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: 8px;
}
.easypanel-panel-link {
  color: var(--theme-color-deep);
  text-decoration: none;
}
.easypanel-panel-link:hover {
  text-decoration: underline;
}
@media (max-width: 820px) {
  .easypanel-grid {
    grid-template-columns: 1fr;
  }
}
/* 账户信息行：标签左、值右对齐；长值省略号 + tooltip；操作按钮挂在值右侧。
   之前站点账号裸 b、站点密码 b+复制、面板地址 a 撑满整行：每行宽度处理不一，对齐混乱。 */
.easypanel-info-row {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: 12px;
}
.easypanel-info-row > span {
  flex: 0 0 auto;
}
.easypanel-info-cell {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  font-weight: 500;
  text-align: right;
}
.easypanel-info-cell__text,
.easypanel-info-cell__link {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
/* 面板地址行独占整行：无复制按钮后放开 65% 上限，地址能多显示一截。 */
.easypanel-info-row .easypanel-info-cell--wide {
  max-width: 100%;
}
.easypanel-info-cell__link {
  color: var(--art-color-primary, #185fa5);
  text-decoration: none;
}
.easypanel-info-cell__link:hover {
  text-decoration: underline;
}
/* 面板地址不截断：放得下就是一行，放不下就换行完整显示。
   截断后只剩域名前缀，用户根本认不出是哪个面板；触屏又没有 hover，title 提示也出不来。
   不按视口宽做断点——真正的约束是「格子里有多少可用宽」：
   phone 档 ~218px、laptop 档只有 ~204px，都会截断，而 1024px 视口并不会命中窄屏断点。
   overflow-wrap: anywhere 必须有：URL 没有空格，默认不会断行，会直接溢出。
   注意这条得写在上面 .easypanel-info-cell__link 的 nowrap 规则之后才压得住。 */
.easypanel-info-cell--wide .easypanel-info-cell__link {
  overflow: visible;
  text-overflow: clip;
  white-space: normal;
  overflow-wrap: anywhere;
}
/* 值占两行时让标签跟首行对齐，而不是浮在两行中间。
   这一行必须同时退出拉伸（flex: 0 0 auto）：行高由内容决定时 flex-start 才等价于居中，
   否则桌面档那行会被拉到 63px（内容只占 19px），标签被顶到行顶、比相邻行高出 12px。
   让出的高度照旧由上面两行（flex: 1 1 auto）继续摊平，不会堆到页脚上方。
   选择器带 .easypanel-info-list 前缀：上面 `.easypanel-info-list > div`（0,1,1）
   的 align-items 优先级高于单个类（0,1,0），不这样写压不过去。 */
.easypanel-info-list > .easypanel-info-row--wide {
  flex: 0 0 auto;
  align-items: flex-start;
}
.easypanel-info-row--wide > span {
  line-height: 1.5;
}
</style>
