<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NIcon,
  NRadioButton,
  NRadioGroup,
  NSpin,
} from 'naive-ui'
import {
  CloudDownloadOutline,
  GitNetworkOutline,
  PeopleOutline,
  PulseOutline,
  RefreshOutline,
} from '@vicons/ionicons5'

import { api } from '@/api'
import type { DashboardData } from '@/api/types'
import PageHeader from '@/components/PageHeader.vue'
import StatCard from '@/components/StatCard.vue'
import TrafficChart from '@/components/TrafficChart.vue'
import { formatBytes, formatCount } from '@/lib/format'

const range = ref<'24h' | '7d' | '30d'>('24h')
const loading = ref(false)
const error = ref('')
const data = ref<DashboardData | null>(null)
let requestId = 0

const stats = computed(() => {
  const summary = data.value?.summary
  return [
    {
      label: '今日请求',
      value: formatCount(summary?.today_requests ?? 0),
      accent: '#6366f1',
      icon: PulseOutline,
    },
    {
      label: '今日流量',
      value: formatBytes(summary?.today_bytes ?? 0),
      accent: '#0ea5e9',
      icon: CloudDownloadOutline,
    },
    {
      label: '代理用户',
      value: formatCount(summary?.total_users ?? 0),
      accent: '#10b981',
      icon: PeopleOutline,
    },
    {
      label: '启用规则',
      value: formatCount(summary?.enabled_rules ?? 0),
      accent: '#f59e0b',
      icon: GitNetworkOutline,
    },
  ]
})

async function load() {
  const currentRequest = ++requestId
  loading.value = true
  error.value = ''
  try {
    const result = await api.dashboard(range.value)
    if (currentRequest === requestId) {
      data.value = result
    }
  } catch (err) {
    if (currentRequest === requestId) {
      error.value = err instanceof Error ? err.message : '加载统计数据失败'
    }
  } finally {
    if (currentRequest === requestId) {
      loading.value = false
    }
  }
}

function changeRange(value: string | number) {
  const next = String(value) as '24h' | '7d' | '30d'
  if (next === range.value) {
    return
  }
  range.value = next
  void load()
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="概览">
      <n-button secondary :loading="loading" @click="load">
        <template #icon>
          <n-icon><RefreshOutline /></n-icon>
        </template>
        刷新
      </n-button>
    </PageHeader>

    <n-alert v-if="error" type="error" :bordered="false" style="margin-bottom: 16px">
      <template #header>统计数据加载失败</template>
      <div class="alert-content">
        <span>{{ error }}</span>
        <n-button size="small" @click="load">重试</n-button>
      </div>
    </n-alert>

    <div class="stats-grid">
      <StatCard
        v-for="item in stats"
        :key="item.label"
        :label="item.label"
        :value="item.value"
        :accent="item.accent"
        :icon="item.icon"
      />
    </div>

    <n-card :bordered="false" class="soft-card">
      <template #header>
        <div class="card-head">
          <div class="card-title">流量趋势</div>
          <n-radio-group :value="range" size="small" @update:value="changeRange">
            <n-radio-button value="24h">24 小时</n-radio-button>
            <n-radio-button value="7d">7 天</n-radio-button>
            <n-radio-button value="30d">30 天</n-radio-button>
          </n-radio-group>
        </div>
      </template>

      <n-spin :show="loading">
        <TrafficChart :points="data?.series ?? []" :range="range" />
      </n-spin>
    </n-card>
  </div>
</template>

<style scoped>
.alert-content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 16px;
}

.soft-card {
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), inset 0 0 0 1px rgba(148, 163, 184, 0.1);
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
}

.card-title {
  font-size: 15px;
  font-weight: 650;
  letter-spacing: -0.01em;
}

@media (max-width: 1180px) {
  .stats-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .stats-grid {
    grid-template-columns: 1fr;
  }
}
</style>
