<!-- 用户菜单（LumeIDC 后台版） -->
<template>
  <ElPopover
    ref="userMenuPopover"
    placement="bottom-end"
    :width="240"
    :hide-after="0"
    :offset="10"
    trigger="hover"
    :show-arrow="false"
    popper-class="user-menu-popover"
    popper-style="padding: 5px 16px;"
  >
    <template #reference>
      <div class="user-avatar">
        <el-icon><UserFilled /></el-icon>
      </div>
    </template>
    <template #default>
      <div class="pt-3">
        <div class="flex-c pb-1 px-0">
          <div class="user-avatar user-avatar--lg">
            <el-icon><UserFilled /></el-icon>
          </div>
          <div class="w-[calc(100%-60px)] h-full">
            <span class="block text-sm font-medium text-g-800 truncate">{{
              userInfo.userName
            }}</span>
            <span class="block mt-0.5 text-xs text-g-500 truncate">{{ userInfo.email }}</span>
          </div>
        </div>
        <ul class="py-4 mt-3 border-t border-g-300/80">
          <li class="btn-item" @click="goPage('/password')">
            <ArtSvgIcon icon="ri:user-settings-line" />
            <span>账户设置</span>
          </li>
          <li class="btn-item" @click="openFrontend">
            <ArtSvgIcon icon="ri:external-link-line" />
            <span>返回前台</span>
          </li>
          <div class="w-full h-px my-2 bg-g-300/80"></div>
          <div class="log-out c-p" @click="loginOut">退出登录</div>
        </ul>
      </div>
    </template>
  </ElPopover>
</template>

<script setup lang="ts">
  import { useRouter } from 'vue-router'
  import { ElMessageBox } from 'element-plus'
  import { UserFilled } from '@element-plus/icons-vue'
  import { useUserStore } from '@/store/modules/user'

  defineOptions({ name: 'ArtUserMenu' })

  const router = useRouter()
  const userStore = useUserStore()

  const { getUserInfo: userInfo } = storeToRefs(userStore)
  const userMenuPopover = ref()

  const goPage = (path: string): void => {
    closeUserMenu()
    router.push(path)
  }

  /** 新窗口打开同源前台门户（后台挂载在 /admin 下，前台在站点根）。 */
  const openFrontend = (): void => {
    closeUserMenu()
    window.open(`${window.location.origin}/`, '_blank')
  }

  const loginOut = (): void => {
    closeUserMenu()
    setTimeout(() => {
      ElMessageBox.confirm('确定要退出登录吗？', '提示', {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        customClass: 'login-out-dialog',
      }).then(() => {
        userStore.logOut()
      })
    }, 200)
  }

  const closeUserMenu = (): void => {
    setTimeout(() => {
      userMenuPopover.value?.hide()
    }, 100)
  }
</script>

<style scoped>
  @reference '@styles/core/tailwind.css';

  @layer components {
    .btn-item {
      @apply flex items-center p-2 mb-3 select-none rounded-md cursor-pointer last:mb-0;

      span {
        @apply text-sm;
      }

      .art-svg-icon {
        @apply mr-2 text-base;
      }

      &:hover {
        background-color: var(--art-gray-200);
      }
    }
  }

  .user-avatar {
    @apply flex items-center justify-center w-8.5 h-8.5 mr-5 cursor-pointer rounded-full max-sm:w-6.5 max-sm:h-6.5 max-sm:mr-[16px];
    color: #fff;
    background: var(--theme-color);
  }

  .user-avatar--lg {
    @apply w-10 h-10 mr-3 ml-0;
  }

  .log-out {
    @apply py-1.5
    mt-5
    text-xs
    text-center
    border
    border-g-400
    rounded-md
    transition-all
    duration-200
    hover:shadow-xl;
  }
</style>
