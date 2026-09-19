<!-- 插件配置自动表单：按后端 ConfigSchema 渲染，零插件前端代码。 -->
<template>
  <el-card v-if="schema.length > 0" shadow="never" v-loading="loading">
    <template #header>{{ title || '插件配置' }}</template>
    <el-form label-width="120px" style="max-width: 640px">
      <el-form-item v-for="f in schema" :key="f.key" :label="f.title">
        <el-switch v-if="f.type === 'switch'" v-model="form[f.key]" />
        <el-input-number v-else-if="f.type === 'number'" v-model="form[f.key]" />
        <el-select v-else-if="f.type === 'select'" v-model="form[f.key]" clearable style="width: 100%">
          <el-option v-for="o in f.options" :key="o.value" :value="o.value" :label="o.label" />
        </el-select>
        <el-checkbox-group v-else-if="f.type === 'multiselect'" v-model="form[f.key]">
          <el-checkbox v-for="o in f.options" :key="o.value" :value="o.value">{{ o.label }}</el-checkbox>
        </el-checkbox-group>
        <el-input
          v-else-if="f.type === 'password'"
          v-model="form[f.key]"
          type="password"
          show-password
          :placeholder="has['has_' + f.key] ? '已配置，留空则不修改' : ''"
        />
        <el-input v-else-if="f.type === 'textarea'" v-model="form[f.key]" type="textarea" :rows="3" />
        <el-input v-else v-model="form[f.key]" />
        <div v-if="f.tip" class="text-xs opacity-60 leading-5 mt-1">{{ f.tip }}</div>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </el-form-item>
    </el-form>
  </el-card>
</template>

<script setup lang="ts">
  import { onMounted, reactive, ref } from 'vue'
  import { ElMessage } from 'element-plus'
  import { http } from '@/http/index'

  interface ConfigOption {
    value: string
    label: string
  }
  interface ConfigField {
    key: string
    title: string
    type: string
    options?: ConfigOption[]
    optionsRef?: string
    tip?: string
    default?: string
  }

  const props = defineProps<{ pluginName: string; title?: string }>()

  const loading = ref(false)
  const saving = ref(false)
  const schema = ref<ConfigField[]>([])
  const has = ref<Record<string, boolean>>({})
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- 表单值异构（布尔/数字/数组/字符串）
  const form = reactive<Record<string, any>>({})

  // 存储字符串 → 表单值（switch "1"→true；number→数字；multiselect JSON→数组）
  function toFormValue(f: ConfigField, raw: string): unknown {
    switch (f.type) {
      case 'switch':
        return raw === '1'
      case 'number':
        return raw === '' ? undefined : Number(raw)
      case 'multiselect':
        try {
          return raw ? (JSON.parse(raw) as string[]) : []
        } catch {
          return []
        }
      default:
        return raw
    }
  }

  async function load() {
    loading.value = true
    try {
      const res = await http.get<{
        ok: number
        schema?: ConfigField[]
        values?: Record<string, string>
        has?: Record<string, boolean>
      }>(`/plugin/${props.pluginName}/config`)
      schema.value = res.schema || []
      has.value = res.has || {}
      for (const f of schema.value) {
        form[f.key] = toFormValue(f, res.values?.[f.key] ?? f.default ?? '')
      }
    } catch {
      schema.value = [] // 无配置能力（或接口不可用）：本组件整体隐藏
    } finally {
      loading.value = false
    }
  }

  async function save() {
    saving.value = true
    try {
      const res = await http.post<{ ok: number; msg?: string }>(`/plugin/${props.pluginName}/config`, {
        values: form,
      })
      if (!res.ok) throw new Error(res.msg || '保存失败')
      ElMessage.success('已保存')
      // password 保存后清空输入框并刷新 has 标记
      for (const f of schema.value) {
        if (f.type === 'password') form[f.key] = ''
      }
      await load()
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '保存失败')
    } finally {
      saving.value = false
    }
  }

  onMounted(load)
</script>
