import { onBeforeUnmount, ref, type Ref } from 'vue'
import { ElMessage } from 'element-plus'

// 每个列表独立计代；离开页面后，操作回调也不能再发起列表查询。
export function useAdminRequest() {
  let sequence = 0
  let disposed = false
  onBeforeUnmount(() => { disposed = true; sequence++ })
  return () => {
    if (disposed) return null
    const current = ++sequence
    return () => !disposed && current === sequence
  }
}

/**
 * 后台"全量拉取 + 前端过滤 + 前端分页"列表页骨架（AdminRefunds/AdminLogs 等同构页共用）。
 * 收敛 page/list/loading/searchForm 状态与 handleSearch/handleReset/handleCurrentChange 样板；
 * 过滤逻辑（filtered）与 pagination（基于 filtered 长度）由页面自行实现。
 * 服务端分页页（AdminOrders/AdminUsers 等）参数与副作用各异，不适用本骨架。
 */
export function useAdminFrontTable<T, F extends Record<string, string>>(
  fetcher: () => Promise<T[]>,
  defaultFilters: F,
  per = 20,
) {
  const list = ref([]) as Ref<T[]>
  const loading = ref(false)
  const page = ref(1)
  const searchForm = ref({ ...defaultFilters }) as Ref<F>
  const startRequest = useAdminRequest()

  async function load() {
    const isCurrent = startRequest()
    if (!isCurrent) return
    loading.value = true
    try {
      const data = await fetcher()
      if (isCurrent()) list.value = data
    } catch (err: unknown) {
      if (isCurrent()) ElMessage.error((err as Error).message || '查询失败')
    } finally {
      if (isCurrent()) loading.value = false
    }
  }

  function handleSearch() {
    page.value = 1
  }

  function handleReset() {
    searchForm.value = { ...defaultFilters }
    page.value = 1
  }

  function handleCurrentChange(p: number) {
    page.value = p
  }

  return { list, loading, page, per, searchForm, load, handleSearch, handleReset, handleCurrentChange }
}
