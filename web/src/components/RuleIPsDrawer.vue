<script setup lang="ts">
import { computed, h, reactive, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NDataTable,
  NDrawer,
  NDrawerContent,
  NEmpty,
  NIcon,
  NSpin,
  NSwitch,
  NTag,
  useMessage,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { CreateOutline, RefreshOutline } from '@vicons/ionicons5'

import { api } from '@/api'
import type { Rule, RuleIP, RuleIPRegion, RuleResolution, RuleTargetScheme } from '@/api/types'
import { formatDateTime, formatDuration } from '@/lib/format'

const props = defineProps<{
  show: boolean
  rule: Rule | null
}>()

const emit = defineEmits<{
  (event: 'update:show', value: boolean): void
  (event: 'edit', rule: Rule): void
}>()

const message = useMessage()
const loading = ref(false)
const refreshing = ref(false)
const resolution = ref<RuleResolution | null>(null)
const togglingIP = ref('')
const regionPagination = reactive({ page: 1, pageSize: 10 })

watch(
  () => props.show,
  (visible) => {
    if (visible && props.rule) {
      regionPagination.page = 1
      void load()
    }
  },
)

async function load() {
  const rule = props.rule
  if (!rule) {
    return
  }
  loading.value = true
  resolution.value = null
  try {
    resolution.value = await api.rules.ips(rule.id)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '加载 IP 失败')
  } finally {
    loading.value = false
  }
}

async function refresh() {
  const rule = props.rule
  if (!rule || refreshing.value) {
    return
  }
  refreshing.value = true
  try {
    resolution.value = await api.rules.refreshIps(rule.id)
    regionPagination.page = 1
    if (resolution.value.error) {
      message.warning(resolution.value.error)
    } else {
      message.success('IP 已更新')
    }
  } catch (error) {
    message.error(error instanceof Error ? error.message : '刷新失败')
  } finally {
    refreshing.value = false
  }
}

async function toggleIP(ip: string, enabled: boolean) {
  const rule = props.rule
  if (!rule || togglingIP.value) {
    return
  }
  togglingIP.value = ip
  try {
    await api.rules.setIpEnabled(rule.id, ip, enabled)
    const item = resolution.value?.ips.find((entry) => entry.ip === ip)
    if (item) {
      item.disabled = !enabled
    }
    message.success(enabled ? 'IP 已启用' : 'IP 已停用')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '更新 IP 状态失败')
  } finally {
    togglingIP.value = ''
  }
}

function editRule() {
  const rule = props.rule
  if (!rule) {
    return
  }
  emit('edit', rule)
}

function displayHost(host: string): string {
  return host.includes(':') ? `[${host}]` : host
}

function targetSchemeLabel(scheme: RuleTargetScheme): string {
  return scheme === 'both' ? '兼容' : scheme.toUpperCase()
}

function upstreamScheme(rule: Rule): 'http' | 'https' {
  if (rule.upstream_scheme === 'follow') {
    return rule.target_scheme === 'http' ? 'http' : 'https'
  }
  return rule.upstream_scheme
}

const accessLabel = computed(() => {
  const rule = props.rule
  if (!rule) {
    return ''
  }
  return `${targetSchemeLabel(rule.target_scheme)} ${displayHost(rule.match_host)}:${rule.match_port}`
})

const upstreamLabel = computed(() => {
  const rule = props.rule
  if (!rule) {
    return ''
  }
  return `${upstreamScheme(rule).toUpperCase()} ${displayHost(rule.upstream_host)}:${rule.upstream_port}`
})

const ipCount = computed(() => resolution.value?.ips.length ?? 0)
const enabledCount = computed(
  () => resolution.value?.ips.filter((item) => !item.disabled).length ?? 0,
)
const reachableCount = computed(
  () => resolution.value?.ips.filter((item) => item.reachable).length ?? 0,
)
const regionTotal = computed(
  () => (resolution.value?.region_ok ?? 0) + (resolution.value?.region_failed ?? 0),
)
const resolvedAt = computed(() => formatDateTime(resolution.value?.resolved_at))

const ipColumns = computed<DataTableColumns<RuleIP>>(() => [
  {
    title: 'IP 地址',
    key: 'ip',
    render: (row) => h('span', { class: 'mono' }, row.ip),
  },
  {
    title: '延迟',
    key: 'latency_ms',
    width: 90,
    render: (row) => formatDuration(row.latency_ms),
  },
  {
    title: '状态',
    key: 'reachable',
    width: 86,
    render: (row) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: row.reachable ? 'success' : 'error' },
        { default: () => (row.reachable ? '可达' : '不可达') },
      ),
  },
  {
    title: '启用',
    key: 'enabled',
    width: 76,
    render: (row) =>
      h(NSwitch, {
        size: 'small',
        value: !row.disabled,
        loading: togglingIP.value === row.ip,
        onUpdateValue: (value: boolean) => void toggleIP(row.ip, value),
      }),
  },
])

const regionColumns = computed<DataTableColumns<RuleIPRegion>>(() => [
  {
    title: '区域',
    key: 'subnet',
    width: 150,
    render: (row) => h('span', { class: 'mono' }, row.subnet),
  },
  {
    title: '结果',
    key: 'failed',
    width: 78,
    render: (row) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: row.failed ? 'error' : 'success' },
        { default: () => (row.failed ? '失败' : '成功') },
      ),
  },
  {
    title: '耗时',
    key: 'latency_ms',
    width: 90,
    render: (row) => formatDuration(row.latency_ms),
  },
  {
    title: 'IP 地址',
    key: 'ips',
    render: (row) => {
      if (row.ips.length === 0) {
        return h('span', { class: 'muted' }, '—')
      }
      return h(
        'div',
        { class: 'ip-tags' },
        row.ips.map((ip) =>
          h(NTag, { size: 'small', bordered: false, class: 'mono' }, { default: () => ip }),
        ),
      )
    },
  },
])

function ipRowKey(row: RuleIP) {
  return row.ip
}

function regionRowKey(row: RuleIPRegion) {
  return row.subnet
}
</script>

<template>
  <n-drawer
    :show="show"
    placement="right"
    width="min(760px, 100vw)"
    @update:show="emit('update:show', $event)"
  >
    <n-drawer-content :title="rule?.name || 'IP 管理'" closable>
      <n-spin :show="loading">
        <div class="drawer-body">
          <div class="endpoint-block">
            <div class="endpoint-row">
              <span class="endpoint-label">访问地址</span>
              <span class="mono endpoint-value">{{ accessLabel }}</span>
            </div>
            <div class="endpoint-row">
              <span class="endpoint-label">转发地址</span>
              <span class="mono endpoint-value">{{ upstreamLabel }}</span>
            </div>
          </div>

          <div class="section-head">
            <div class="section-title">IP 地址</div>
            <div class="section-actions">
              <span v-if="resolution" class="muted section-meta">{{ resolvedAt }}</span>
              <n-button size="small" secondary :loading="refreshing" @click="refresh">
                <template #icon>
                  <n-icon><RefreshOutline /></n-icon>
                </template>
                刷新
              </n-button>
            </div>
          </div>

          <n-alert
            v-if="resolution?.error"
            type="warning"
            class="error-alert"
          >
            {{ resolution.error }}
          </n-alert>

          <div v-if="resolution" class="stats">
            <span>{{ ipCount }} 个 IP</span>
            <span>启用 {{ enabledCount }}</span>
            <span>可达 {{ reachableCount }}</span>
            <span>区域 {{ resolution.region_ok }}/{{ regionTotal }}</span>
          </div>

          <n-data-table
            size="small"
            :columns="ipColumns"
            :data="resolution?.ips ?? []"
            :pagination="false"
            :bordered="false"
            :single-line="false"
            :scroll-x="420"
            :row-key="ipRowKey"
          >
            <template #empty>
              <n-empty description="暂无 IP" />
            </template>
          </n-data-table>

          <div class="section-head region-head">
            <div class="section-title">区域解析</div>
            <span v-if="resolution" class="muted section-meta">
              {{ resolution.regions.length }} 个区域
            </span>
          </div>

          <n-data-table
            size="small"
            :columns="regionColumns"
            :data="resolution?.regions ?? []"
            :pagination="regionPagination"
            :bordered="false"
            :single-line="false"
            :scroll-x="560"
            :row-key="regionRowKey"
            @update:page="regionPagination.page = $event"
          >
            <template #empty>
              <n-empty description="暂无解析结果" />
            </template>
          </n-data-table>
        </div>
      </n-spin>

      <template #footer>
        <div class="drawer-footer">
          <n-button type="primary" :disabled="!rule" @click="editRule">
            <template #icon>
              <n-icon><CreateOutline /></n-icon>
            </template>
            编辑规则
          </n-button>
        </div>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.drawer-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.endpoint-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border-radius: 10px;
  background: rgba(148, 163, 184, 0.1);
}

.endpoint-row {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.endpoint-label {
  flex: 0 0 auto;
  width: 60px;
  font-size: 12px;
  opacity: 0.62;
}

.endpoint-value {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12.5px;
}

.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 30px;
}

.section-title {
  font-size: 13px;
  font-weight: 650;
}

.section-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.section-meta {
  font-size: 12px;
}

.region-head {
  margin-top: 4px;
}

.stats {
  display: flex;
  gap: 16px;
  font-size: 12.5px;
  opacity: 0.72;
}

.error-alert {
  margin-bottom: 2px;
}

.ip-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.drawer-footer {
  display: flex;
  justify-content: flex-end;
}

@media (max-width: 560px) {
  .endpoint-label {
    width: 52px;
  }
}
</style>
