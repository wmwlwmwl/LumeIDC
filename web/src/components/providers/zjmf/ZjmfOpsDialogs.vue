<script setup lang="ts">
/**
 * ZJMF 维护弹窗组：重装系统 / 重置密码 / 救援模式三件套。
 * 父组件通过 ref 调用 openReinstall()/openPassDialog()/openRescue() 打开；
 * 救援成功后 emit op-done 通知父级刷新面板。
 */
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  fetchReinstallOptions, fetchRescueState, reinstallService, resetServicePassword, rescueService,
  type OSOption, type PowerInfo,
} from '../../../api/user'

const props = defineProps<{ serviceId: number; powerState: PowerInfo | null }>()
const emit = defineEmits<{ 'op-done': []; 'rescue-change': [on: boolean] }>()

const opBusy = ref(false)
const osOptions = ref<OSOption[]>([])
const reinstallDialog = ref(false)
const osGroup = ref('')
const osChosen = ref('')
const passDialog = ref(false)
const newPass = ref('')
const rescueDialog = ref(false)
const rescueSystem = ref('2')
const rescuePass = ref('')
const rescueForce = ref(false)

const osGroups = computed(() => [...new Set(osOptions.value.map((o) => o.group || '其他'))])
const groupedOSOptions = computed(() => osOptions.value.filter((o) => (o.group || '其他') === osGroup.value))
watch(osGroup, () => { osChosen.value = '' })

async function openReinstall() {
  if (!osOptions.value.length) {
    osOptions.value = await fetchReinstallOptions(props.serviceId).catch(() => [])
    osGroup.value = osOptions.value[0]?.group || (osOptions.value.length ? '其他' : '')
  }
  osChosen.value = ''
  reinstallDialog.value = true
}

async function openRescue() {
  const on = await fetchRescueState(props.serviceId).catch(() => false)
  emit('rescue-change', on)
  rescueDialog.value = true
}

function openPassDialog() {
  newPass.value = ''
  passDialog.value = true
}

async function doReinstall() {
  if (!osChosen.value) {
    ElMessage.warning('请选择要安装的系统')
    return
  }
  if (
    !(await ElMessageBox.confirm('重装系统将清空实例数据，确认继续？', '重装系统', {
      type: 'warning',
      confirmButtonText: '确认重装',
    }).catch(() => null))
  )
    return
  opBusy.value = true
  try {
    const res = await reinstallService(props.serviceId, osChosen.value)
    if (String(res.ok) === '1') {
      ElMessage.success(res.msg || '已下发重装')
      reinstallDialog.value = false
    } else {
      ElMessage.error(res.msg || '重装失败')
    }
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '重装失败')
  } finally {
    opBusy.value = false
  }
}

async function doResetPassword() {
  opBusy.value = true
  try {
    const res = await resetServicePassword(props.serviceId, newPass.value)
    if (String(res.ok) === '1') {
      await ElMessageBox.alert(res.msg || '密码已重置', '新密码', {
        confirmButtonText: '我已记录',
      })
      passDialog.value = false
      newPass.value = ''
    } else {
      ElMessage.error(res.msg || '重置失败')
    }
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '重置失败')
  } finally {
    opBusy.value = false
  }
}

async function doRescue() {
  if ((props.powerState?.status === 'on' || props.powerState?.status === 'operating') && !rescueForce.value) {
    ElMessage.warning('实例处于开机状态，请同意强制关机或先手动关机。')
    return
  }
  opBusy.value = true
  try {
    const res = await rescueService(props.serviceId, rescueSystem.value, rescuePass.value.trim())
    if (String(res.ok) === '1') {
      await ElMessageBox.alert(res.msg || '救援系统已启动', '救援模式', {
        confirmButtonText: '我已记录',
      })
      rescueDialog.value = false
      rescuePass.value = ''
      rescueForce.value = false
      emit('rescue-change', true)
      emit('op-done')
    } else {
      ElMessage.error(res.msg || '启动救援失败')
    }
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '启动救援失败')
  } finally {
    opBusy.value = false
  }
}

defineExpose({ openReinstall, openPassDialog, openRescue })
</script>

<template>
  <!-- ========== 弹窗：重装系统 ========== -->
  <el-dialog v-model="reinstallDialog" title="重装系统" width="460px">
    <el-alert title="重装会清空实例数据，请先备份。" type="warning" :closable="false" show-icon class="zjmf-dialog-alert" />
    <div class="zjmf-reinstall-fields">
      <el-select v-model="osGroup" placeholder="选择系统类型">
        <el-option v-for="group in osGroups" :key="group" :value="group" :label="group" />
      </el-select>
      <el-select v-model="osChosen" filterable placeholder="选择系统版本">
        <el-option v-for="o in groupedOSOptions" :key="o.id" :value="o.id" :label="o.name" />
      </el-select>
    </div>
    <template #footer>
      <el-button @click="reinstallDialog = false">取消</el-button>
      <el-button type="primary" :loading="opBusy" @click="doReinstall">确认重装</el-button>
    </template>
  </el-dialog>

  <!-- ========== 弹窗：重置密码 ========== -->
  <el-dialog v-model="passDialog" title="重置实例密码" width="420px">
    <el-input v-model="newPass" placeholder="留空则由系统生成强密码" />
    <template #footer>
      <el-button @click="passDialog = false">取消</el-button>
      <el-button type="primary" :loading="opBusy" @click="doResetPassword">确认重置</el-button>
    </template>
  </el-dialog>

  <!-- ========== 弹窗：救援模式 ========== -->
  <el-dialog v-model="rescueDialog" title="进入救援模式" width="420px">
    <el-radio-group v-model="rescueSystem" class="mb-3">
      <el-radio value="2">Linux</el-radio>
      <el-radio value="1">Windows</el-radio>
    </el-radio-group>
    <el-input v-model="rescuePass" placeholder="临时密码（留空自动生成）" />
    <div
      v-if="powerState?.status === 'on' || powerState?.status === 'operating'"
      class="zjmf-rescue-force"
    >
      <p>当前操作需要实例在关机状态下进行，强制关机可能造成数据丢失。</p>
      <el-checkbox v-model="rescueForce">同意强制关机</el-checkbox>
    </div>
    <template #footer>
      <el-button @click="rescueDialog = false">取消</el-button>
      <el-button type="primary" :loading="opBusy" @click="doRescue">启动救援</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
/* --- 弹窗内部警告 --- */
.zjmf-dialog-alert {
  margin-bottom: 0;
}
.zjmf-reinstall-fields { display: grid; grid-template-columns: 0.8fr 1.2fr; gap: 10px; margin-top: 14px; }

/* --- 救援强制关机提示 --- */
.zjmf-rescue-force {
  margin-top: 14px;
  padding: 12px 14px;
  background: color-mix(in srgb, var(--el-color-warning) 8%, transparent);
  border: 1px solid color-mix(in srgb, var(--el-color-warning) 20%, transparent);
  border-radius: 8px;
  font-size: 12px;
  color: var(--art-gray-700);
}
.zjmf-rescue-force p {
  margin: 0 0 8px;
}

@media (max-width: 420px) { .zjmf-reinstall-fields { grid-template-columns: 1fr; } }
</style>
