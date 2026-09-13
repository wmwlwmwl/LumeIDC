<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { Monitor, ArrowDown, Check } from '@element-plus/icons-vue'

defineOptions({ name: 'OsSelector' })

interface OsSub {
  name: string
  value?: string
  min?: number
  max?: number
  pricing?: Record<string, number>
}

const props = defineProps<{
  subs: OsSub[]
  modelValue: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', v: string): void
}>()

const FAMILIES = ['CentOS', 'Debian', 'Ubuntu', 'Fedora', 'Rocky', 'AlmaLinux', 'OpenEuler'] as const

function familyOf(name: string): string {
  const n = name.toLowerCase()
  if (n.startsWith('centos')) return 'CentOS'
  if (n.startsWith('debian')) return 'Debian'
  if (n.startsWith('ubuntu')) return 'Ubuntu'
  if (n.startsWith('fedora')) return 'Fedora'
  if (n.startsWith('rocky')) return 'Rocky'
  if (n.startsWith('almalinux')) return 'AlmaLinux'
  if (n.includes('openeuler')) return 'OpenEuler'
  return '其他'
}
const valueOf = (s: OsSub): string => s.value || s.name

const groups = computed(() => {
  const map = new Map<string, OsSub[]>()
  for (const s of props.subs) {
    const f = familyOf(s.name)
    if (!map.has(f)) map.set(f, [])
    map.get(f)!.push(s)
  }
  const order = [...FAMILIES, '其他']
  return order.filter((f) => map.has(f)).map((f) => ({ family: f, versions: map.get(f)! }))
})

const selectedFamily = computed(() => {
  const s = props.subs.find((x) => valueOf(x) === props.modelValue)
  return s ? familyOf(s.name) : ''
})
const selectedName = computed(() => props.subs.find((x) => valueOf(x) === props.modelValue)?.name || '')

const openFamily = ref('')
function toggle(f: string) {
  openFamily.value = openFamily.value === f ? '' : f
}
function pick(s: OsSub) {
  emit('update:modelValue', valueOf(s))
  openFamily.value = ''
}

const root = ref<HTMLElement | null>(null)
function onDocClick(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) openFamily.value = ''
}
onMounted(() => document.addEventListener('pointerdown', onDocClick))
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocClick))

function focusFirstOption() {
  void nextTick(() => {
    root.value?.querySelector<HTMLElement>('.os-dropdown .os-option')?.focus()
  })
}
function onHeadKeydown(e: KeyboardEvent, f: string) {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    openFamily.value = f
    focusFirstOption()
  } else if (e.key === 'Escape') {
    openFamily.value = ''
  }
}
function onOptionKeydown(e: KeyboardEvent, s: OsSub) {
  const list = Array.from(root.value?.querySelectorAll<HTMLElement>('.os-dropdown .os-option') || [])
  const idx = list.indexOf(e.currentTarget as HTMLElement)
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    list[(idx + 1) % list.length]?.focus()
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    list[(idx - 1 + list.length) % list.length]?.focus()
  } else if (e.key === 'Escape') {
    e.preventDefault()
    openFamily.value = ''
    ;(e.currentTarget as HTMLElement)
      .closest('.os-card')
      ?.querySelector<HTMLElement>('.os-card__head')
      ?.focus()
  } else if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    pick(s)
  }
}
</script>

<template>
  <div ref="root" class="os-selector">
    <div class="os-grid">
      <div
        v-for="g in groups"
        :key="g.family"
        class="os-card"
        :class="{ 'is-active': selectedFamily === g.family, 'is-open': openFamily === g.family }"
      >
        <button
          type="button"
          class="os-card__head"
          aria-haspopup="listbox"
          :aria-expanded="openFamily === g.family"
          @click="toggle(g.family)"
          @keydown="onHeadKeydown($event, g.family)"
        >
          <span class="os-card__icon"><el-icon><Monitor /></el-icon></span>
          <span class="os-card__name">{{ g.family }}</span>
          <el-icon class="os-card__arrow"><ArrowDown /></el-icon>
        </button>
        <div class="os-card__foot">
          {{ selectedFamily === g.family && selectedName ? selectedName : '选择版本' }}
        </div>

        <div v-if="openFamily === g.family" class="os-dropdown" role="listbox" :aria-label="g.family">
          <button
            v-for="v in g.versions"
            :key="valueOf(v)"
            type="button"
            class="os-option"
            role="option"
            :aria-selected="valueOf(v) === modelValue"
            :class="{ 'is-selected': valueOf(v) === modelValue }"
            @click="pick(v)"
            @keydown="onOptionKeydown($event, v)"
          >
            <span class="os-option__label">{{ v.name }}</span>
            <el-icon v-if="valueOf(v) === modelValue" class="os-option__check"><Check /></el-icon>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.os-selector {
  width: 100%;
}

.os-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 12px;
}

.os-card {
  position: relative;
  overflow: visible;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.os-card:hover {
  border-color: color-mix(in srgb, var(--theme-color) 50%, var(--art-card-border));
}

.os-card.is-active {
  border-color: var(--theme-color);
  box-shadow: 0 0 0 1px var(--theme-color) inset;
}

.os-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  background: transparent;
  border: 0;
  cursor: pointer;
}

.os-card__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  color: var(--art-gray-400);
  font-size: 20px;
}

.os-card.is-active .os-card__icon {
  color: var(--theme-color);
}

.os-card__name {
  color: var(--art-gray-800);
  font-size: 14px;
  font-weight: 600;
}

.os-card.is-active .os-card__name {
  color: var(--theme-color);
}

.os-card__arrow {
  margin-left: auto;
  color: var(--art-gray-400);
  font-size: 12px;
  transition: transform 0.2s ease;
}

.os-card.is-open .os-card__arrow {
  transform: rotate(180deg);
}

.os-card__foot {
  padding: 10px 12px;
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
  border-top: 1px solid var(--art-card-border);
}

.os-card.is-active .os-card__foot {
  color: var(--art-gray-800);
  font-weight: 600;
}

.os-dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 60;
  width: max(100%, 190px);
  max-height: 280px;
  overflow: auto;
  padding: 6px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  box-shadow: 0 12px 32px color-mix(in srgb, var(--art-gray-900) 14%, transparent);
}

.os-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 9px 10px;
  color: var(--art-gray-700);
  font-size: 13px;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.os-option__check {
  color: var(--theme-color);
  font-size: 13px;
}

.os-option:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
}

.os-option.is-selected {
  color: var(--theme-color);
  font-weight: 600;
  background: var(--theme-color-soft);
}
</style>
