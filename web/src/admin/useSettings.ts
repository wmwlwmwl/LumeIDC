import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { fetchAdminSettings, saveAdminSettings } from './api'

/**
 * 后台设置页共享逻辑：一次拉取全量 cfg，按 settings_section 分区保存。
 * 三个设置页（通知/登录与验证/实名）共用，避免重复的 on/flag/flags/save。
 */
export function useAdminSettings() {
  const loading = ref(true)
  const saving = ref('')
  const cfg = reactive<Record<string, any>>({})

  async function load() {
    loading.value = true
    try {
      const data = await fetchAdminSettings()
      for (const k of Object.keys(data)) cfg[k] = data[k]
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '读取设置失败')
    } finally {
      loading.value = false
    }
  }

  /** boolean/任意值 → "1"/"0"。 */
  function flag(key: string): string {
    return cfg[key] === '1' ? '1' : '0'
  }

  /** 把一组开关按键名收集为 1/0。 */
  function flags(keys: string[]): Record<string, string> {
    const out: Record<string, string> = {}
    for (const k of keys) out[k] = flag(k)
    return out
  }

  async function save(section: string, body: Record<string, string>, key = section) {
    saving.value = key
    try {
      const res = await saveAdminSettings(section, body)
      // HTTP 200 也可能业务失败（ok=0），不能当成功提示
      const ok = res.ok === true || String(res.ok) === '1'
      if (!ok) {
        ElMessage.error(res.msg || '保存失败')
        return
      }
      ElMessage.success('已保存')
      // 不做全量重拉：load() 会覆盖其它分区尚未保存的表单改动（如通知页同时编辑邮件与短信）
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '保存失败')
    } finally {
      saving.value = ''
    }
  }

  return { loading, saving, cfg, load, save, flag, flags }
}
