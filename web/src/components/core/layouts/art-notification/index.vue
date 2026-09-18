<!-- 通知组件（接入 LumeIDC 后台真实数据：停用申请/实名/工单/公告） -->
<template>
  <div
    class="art-notification-panel art-card-sm !shadow-xl"
    :style="{
      transform: show ? 'scaleY(1)' : 'scaleY(0.9)',
      opacity: show ? 1 : 0
    }"
    v-show="visible"
    @click.stop
  >
    <div class="flex-cb px-3.5 mt-3.5">
      <span class="text-base font-medium text-g-800">{{ $t('notice.title') }}</span>
      <span
        class="text-xs text-g-800 px-1.5 py-1 c-p select-none rounded hover:bg-g-200"
        @click="close"
      >
        {{ $t('notice.btnRead') }}
      </span>
    </div>

    <ul class="box-border flex items-end w-full h-12.5 px-3.5 border-b-d">
      <li
        v-for="(item, index) in barList"
        :key="index"
        class="h-12 leading-12 mr-5 overflow-hidden text-[13px] text-g-700 c-p select-none"
        :class="{ 'bar-active': barActiveIndex === index }"
        @click="changeBar(index)"
      >
        {{ item.name }} ({{ item.num }})
      </li>
    </ul>

    <div class="w-full h-[calc(100%-95px)]">
      <div class="h-[calc(100%-60px)] overflow-y-scroll scrollbar-thin">
        <!-- 通知 -->
        <ul v-show="barActiveIndex === 0">
          <li
            v-for="(item, index) in notices"
            :key="index"
            class="box-border flex-c px-3.5 py-3.5 c-p last:border-b-0 hover:bg-g-200/60"
            @click="go(item)"
          >
            <div
              class="size-9 leading-9 text-center rounded-lg flex-cc"
              :class="[getNoticeStyle(item.type).iconClass]"
            >
              <ArtSvgIcon class="text-lg !bg-transparent" :icon="getNoticeStyle(item.type).icon" />
            </div>
            <div class="w-[calc(100%-45px)] ml-3.5">
              <h4 class="text-sm font-normal leading-5.5 text-g-900">{{ item.title }}</h4>
              <p class="mt-1.5 text-xs text-g-500">{{ item.time }}</p>
            </div>
          </li>
        </ul>

        <!-- 消息 -->
        <ul v-show="barActiveIndex === 1">
          <li
            v-for="(item, index) in messages"
            :key="index"
            class="box-border flex-c px-3.5 py-3.5 c-p last:border-b-0 hover:bg-g-200/60"
            @click="go(item)"
          >
            <div
              class="size-9 leading-9 text-center rounded-lg flex-cc"
              :class="[getNoticeStyle(item.type).iconClass]"
            >
              <ArtSvgIcon class="text-lg !bg-transparent" :icon="getNoticeStyle(item.type).icon" />
            </div>
            <div class="w-[calc(100%-45px)] ml-3.5">
              <h4 class="text-sm font-normal leading-5.5 text-g-900">{{ item.title }}</h4>
              <p class="mt-1.5 text-xs text-g-500">{{ item.time }}</p>
            </div>
          </li>
        </ul>

        <!-- 待办：二级标签按类型切换，一次只显示一类 -->
        <div v-show="barActiveIndex === 2">
          <ul class="box-border flex flex-wrap items-center gap-2 px-3.5 pt-3 pb-2">
            <li
              v-for="g in todoGroups"
              :key="g.type"
              class="px-2.5 py-1 text-xs rounded-md c-p select-none"
              :class="
                currentTodoGroup && currentTodoGroup.type === g.type
                  ? 'bg-theme text-white'
                  : 'bg-g-200 text-g-700'
              "
              @click="activeTodoGroup = g.type"
            >
              {{ g.label }} ({{ g.items.length }})
            </li>
          </ul>
          <ul>
            <li
              v-for="(item, index) in currentTodoItems"
              :key="index"
              class="box-border flex-c px-3.5 py-3.5 c-p last:border-b-0 hover:bg-g-200/60"
              @click="go(item)"
            >
              <div
                class="size-9 leading-9 text-center rounded-lg flex-cc"
                :class="[getNoticeStyle(item.type).iconClass]"
              >
                <ArtSvgIcon class="text-lg !bg-transparent" :icon="getNoticeStyle(item.type).icon" />
              </div>
              <div class="w-[calc(100%-45px)] ml-3.5">
                <h4 class="text-sm font-normal leading-5.5 text-g-900">{{ item.title }}</h4>
                <p class="mt-1.5 text-xs text-g-500">{{ item.time }}</p>
              </div>
            </li>
          </ul>
        </div>

        <!-- 空状态 -->
        <div
          v-show="currentTabIsEmpty"
          class="relative top-25 h-full text-g-500 text-center !bg-transparent"
        >
          <ArtSvgIcon icon="ri:inbox-line" class="text-5xl" />
          <p class="mt-3.5 text-xs !bg-transparent"
            >{{ $t('notice.text[0]') }}{{ barList[barActiveIndex].name }}</p
          >
        </div>
      </div>

      <div class="relative box-border w-full px-3.5">
        <!-- 待办页签：跳当前二级标签选中那类的列表页；「其他」组没有对应页面故禁用 -->
        <ElButton
          class="w-full mt-3"
          :disabled="barActiveIndex === 2 && !currentTodoGroup?.link"
          @click="handleViewAll"
          v-ripple
        >
          {{ $t('notice.viewAll') }}
        </ElButton>
      </div>
    </div>

    <div class="h-25"></div>
  </div>
</template>

<script setup lang="ts">
  import { computed, ref, watch } from 'vue'
  import { useI18n } from 'vue-i18n'
  import { useRouter } from 'vue-router'
  import {
    adminNotifications,
    loadAdminNotifications,
  } from '@/admin/notice'
  import type { AdminNoticeItem } from '@/admin/api'

  defineOptions({ name: 'ArtNotification' })

  const props = defineProps<{
    value: boolean
  }>()

  const emit = defineEmits<{
    'update:value': [value: boolean]
  }>()

  const router = useRouter()
  const { t } = useI18n()

  const show = ref(false)
  const visible = ref(false)
  const barActiveIndex = ref(0)

  const notices = computed(() => adminNotifications.value.notices)
  const messages = computed(() => adminNotifications.value.messages)
  const todos = computed(() => adminNotifications.value.todos)

  const barList = computed(() => [
    { name: t('notice.bar[0]'), num: notices.value.length },
    { name: t('notice.bar[1]'), num: messages.value.length },
    { name: t('notice.bar[2]'), num: todos.value.length }
  ])

  const currentTabIsEmpty = computed(() => {
    const map = [notices.value, messages.value, todos.value]
    return (map[barActiveIndex.value] || []).length === 0
  })

  const NOTICE_STYLES: Record<string, { icon: string; iconClass: string }> = {
    cancel: { icon: 'ri:chat-delete-line', iconClass: 'bg-danger/12 text-danger' },
    verification: { icon: 'ri:shield-check-line', iconClass: 'bg-warning/12 text-warning' },
    ticket: { icon: 'ri:customer-service-2-line', iconClass: 'bg-success/12 text-success' },
    notice: { icon: 'ri:notification-3-line', iconClass: 'bg-theme/12 text-theme' },
    // 开通/续费/升降配失败：需要管理员重试或退款，用告警红
    fulfillment: { icon: 'ri:error-warning-line', iconClass: 'bg-danger/12 text-danger' }
  }

  const getNoticeStyle = (type: string) =>
    NOTICE_STYLES[type] || { icon: 'ri:arrow-right-circle-line', iconClass: 'bg-theme/12 text-theme' }

  const showNotice = (open: boolean) => {
    if (open) {
      visible.value = true
      setTimeout(() => {
        show.value = true
      }, 5)
    } else {
      show.value = false
      setTimeout(() => {
        visible.value = false
      }, 350)
    }
  }

  const changeBar = (index: number) => {
    barActiveIndex.value = index
  }

  const close = () => {
    emit('update:value', false)
  }

  const go = (item: AdminNoticeItem) => {
    close()
    if (item.link) router.push(item.link)
  }

  // 查看全部：按当前标签跳到对应后台页面。
  // 索引 2（待办）走二级标签选中组的列表页——待办横跨多个模块，没有统一的「全部」页。
  const VIEW_ALL_LINKS = ['/announcements', '/tickets', '']

  const handleViewAll = () => {
    const link =
      barActiveIndex.value === 2
        ? currentTodoGroup.value?.link || ''
        : VIEW_ALL_LINKS[barActiveIndex.value]
    close()
    if (link) router.push(link)
  }

  // 待办分组：顺序即展示顺序（故障最紧急在前）。未知的 type 归入「其他」，
  // 保证后端新增待办类型时条目不会整个消失。
  const TODO_GROUPS: { type: string; label: string; link: string }[] = [
    { type: 'fulfillment', label: '履约失败', link: '/services' },
    { type: 'cancel', label: '停用申请', link: '/cancel-requests' },
    { type: 'verification', label: '实名待审', link: '/verifications' },
  ]

  const todoGroups = computed(() => {
    const groups = TODO_GROUPS.map((g) => ({
      ...g,
      items: todos.value.filter((i) => i.type === g.type),
    })).filter((g) => g.items.length > 0)
    const rest = todos.value.filter((i) => !TODO_GROUPS.some((g) => g.type === i.type))
    if (rest.length) groups.push({ type: 'other', label: '其他', link: '', items: rest })
    return groups
  })

  // 二级标签当前选中的组。选中项失效时（该组被清空、切到别的类型）回退到第一组，
  // 避免出现「标签高亮但列表空」；todoGroups 已滤掉空组，所以标签不会点到空列表。
  const activeTodoGroup = ref('')
  const currentTodoGroup = computed(
    () => todoGroups.value.find((g) => g.type === activeTodoGroup.value) || todoGroups.value[0] || null
  )
  const currentTodoItems = computed(() => currentTodoGroup.value?.items ?? [])

  watch(
    () => props.value,
    (newValue) => {
      showNotice(newValue)
      if (newValue) void loadAdminNotifications()
    }
  )
</script>

<style scoped>
  @reference '@styles/core/tailwind.css';

  .art-notification-panel {
    @apply absolute 
    top-14.5 
    right-5 
    w-90 
    h-125 
    overflow-hidden 
    transition-all 
    duration-300
    origin-top 
    will-change-[top,left] 
    max-[640px]:top-[65px]
    max-[640px]:right-0
    max-[640px]:w-full 
    max-[640px]:h-[80vh];
  }

  .bar-active {
    color: var(--theme-color) !important;
    border-bottom: 2px solid var(--theme-color);
  }

  .scrollbar-thin::-webkit-scrollbar {
    width: 5px !important;
  }

  .dark .scrollbar-thin::-webkit-scrollbar-track {
    background-color: var(--default-box-color);
  }

  .dark .scrollbar-thin::-webkit-scrollbar-thumb {
    background-color: #222 !important;
  }
</style>
