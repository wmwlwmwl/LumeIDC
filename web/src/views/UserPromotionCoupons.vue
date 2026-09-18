<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-bold text-slate-800">活动优惠券</h2>
    </div>

    <div v-if="loading" class="text-center py-12 text-slate-400">加载中...</div>
    <div v-else-if="coupons.length === 0" class="text-center py-12">
      <el-icon class="text-5xl text-slate-300 mb-3"><Ticket /></el-icon>
      <p class="text-slate-400">暂无活动优惠券</p>
      <p class="text-sm text-slate-400 mt-1">去活动页面领取优惠券吧</p>
    </div>
    <div v-else class="grid grid-cols-1 md:grid-cols-2 gap-4">
      <div
        v-for="c in coupons"
        :key="c.coupon_code"
        class="relative bg-white rounded-xl border border-slate-200 overflow-hidden flex"
        :class="{ 'opacity-60': c.used }"
      >
        <!-- 左侧金额 -->
        <div class="w-28 bg-gradient-to-br from-rose-500 to-pink-500 text-white p-4 flex flex-col justify-center items-center">
          <div class="text-3xl font-bold">
            <template v-if="c.coupon_type === 'fixed'">¥{{ c.coupon_value }}</template>
            <template v-else>{{ c.coupon_value }}%</template>
          </div>
          <div class="text-xs mt-1 opacity-90">
            <template v-if="c.coupon_type === 'fixed'">满减券</template>
            <template v-else>折扣券</template>
          </div>
        </div>
        <!-- 右侧信息 -->
        <div class="flex-1 p-4 flex flex-col justify-between">
          <div>
            <div class="font-medium text-slate-800">{{ c.promotion_name }}</div>
            <div class="text-xs text-slate-400 mt-1">
              <template v-if="c.min_amount > 0">满 ¥{{ c.min_amount }} 可用</template>
              <template v-else>无门槛</template>
            </div>
          </div>
          <div class="flex items-center justify-between mt-2">
            <div class="text-xs text-slate-400">
              领取于 {{ c.claimed_at }}
              <span v-if="c.expires_at"> · 有效期至 {{ c.expires_at }}</span>
            </div>
            <el-tag :type="c.used ? 'info' : 'success'" size="small">
              {{ c.used ? '已使用' : '可用' }}
            </el-tag>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Ticket } from '@element-plus/icons-vue'
import { http } from '@/http'

interface UserCoupon {
  promotion_id: number
  promotion_name: string
  coupon_code: string
  coupon_type: string
  coupon_value: number
  min_amount: number
  claimed_at: string
  expires_at?: string
  used: boolean
}

const coupons = ref<UserCoupon[]>([])
const loading = ref(true)

async function load() {
  loading.value = true
  try {
    const res = await http.get<{ ok: number; list?: UserCoupon[] }>('/user/promotion-coupons')
    coupons.value = (res.list || []) as UserCoupon[]
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>
