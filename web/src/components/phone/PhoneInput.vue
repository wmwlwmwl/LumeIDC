<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { PHONE_CODES, MAINLAND_RE, splitE164 } from '@/constants/phone-codes'

// 国家/地区区号 + 手机号组合输入。v-model 为完整 E.164 字符串（如 +8613800138000）。
// 中国大陆默认校验 11 位号段；其他区号长度不硬限，由后端 E.164 校验把关。
const props = withDefaults(
  defineProps<{
    modelValue?: string
    placeholder?: string
    disabled?: boolean
  }>(),
  { modelValue: '', placeholder: '请输入手机号码', disabled: false },
)
const emit = defineEmits<{ (e: 'update:modelValue', value: string): void }>()

const code = ref('+86')
const number = ref('')

// 外部 modelValue 变化（含回显）→ 拆分为区号 + 号码；不触发 emit 避免循环
watch(
  () => props.modelValue,
  (val) => {
    const { code: c, number: n } = splitE164(val ?? '')
    code.value = c
    number.value = n
  },
  { immediate: true },
)

// 中国大陆号段限 11 位，其余区号不硬限（交给后端）
const maxDigits = computed(() => (code.value === '+86' ? 11 : undefined))

function onInput(value: string) {
  number.value = value.replace(/\D+/g, '')
  emitValue()
}

function emitValue() {
  emit('update:modelValue', number.value ? code.value + number.value : '')
}

// 供父页面提交前调用：大陆号做号段预校验，其余区号仅要求非空
function check(): { ok: boolean; msg: string } {
  if (!number.value) return { ok: false, msg: '请输入手机号码' }
  if (code.value === '+86' && !MAINLAND_RE.test(number.value)) {
    return { ok: false, msg: '手机号格式不正确' }
  }
  return { ok: true, msg: '' }
}

defineExpose({ check })
</script>

<template>
  <div class="phone-input">
    <el-select
      v-model="code"
      class="phone-input__code"
      filterable
      :disabled="disabled"
      :placeholder="'+86'"
      :aria-label="'国家/地区区号'"
      @change="emitValue"
    >
      <el-option
        v-for="item in PHONE_CODES"
        :key="item.code + item.label"
        :label="`${item.code}【${item.label}】`"
        :value="item.code"
      />
    </el-select>
    <el-input
      v-model="number"
      class="phone-input__number"
      type="tel"
      :placeholder="placeholder"
      :disabled="disabled"
      :maxlength="maxDigits"
      size="large"
      autocomplete="tel-national"
      @input="onInput"
    />
  </div>
</template>

<style scoped>
.phone-input {
  display: flex;
  gap: 8px;
  width: 100%;
  min-width: 0;
}

.phone-input__code {
  flex-shrink: 0;
  width: 108px;
}

.phone-input__code :deep(.el-select__wrapper) {
  padding-inline: 8px;
}

.phone-input__code :deep(.el-select__selected-item) {
  font-size: 13px;
}

.phone-input__number {
  flex: 1;
  min-width: 120px;
}
</style>