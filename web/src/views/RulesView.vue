<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NEmpty,
  NIcon,
  NInput,
  NSwitch,
  useMessage,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { AddOutline, RefreshOutline, SearchOutline } from '@vicons/ionicons5'

import { api } from '@/api'
import type { Rule, RuleTargetScheme, RuleUpstreamScheme } from '@/api/types'
import PageHeader from '@/components/PageHeader.vue'
import RuleFormModal from '@/components/RuleFormModal.vue'

const message = useMessage()

const loading = ref(false)
const rules = ref<Rule[]>([])
const keyword = ref('')
const showForm = ref(false)
const editingRule = ref<Rule | null>(null)
const togglingId = ref<number | null>(null)

const pagination = reactive({
  page: 1,
  pageSize: 20,
  itemCount: 0,
  showSizePicker: true,
  pageSizes: [10, 20, 50, 100],
})

function onPageChange(page: number) {
  pagination.page = page
  void load()
}

function onPageSizeChange(size: number) {
  pagination.pageSize = size
  pagination.page = 1
  void load()
}

function rowKey(row: Rule) {
  return row.id
}

function rowProps(row: Rule) {
  return {
    style: 'cursor: pointer',
    onClick: (event: MouseEvent) => {
      const target = event.target as HTMLElement | null
      if (target?.closest('.n-switch')) {
        return
      }
      openEdit(row)
    },
  }
}

async function load() {
  loading.value = true
  try {
    const result = await api.rules.list({
      page: pagination.page,
      page_size: pagination.pageSize,
      keyword: keyword.value.trim(),
    })
    rules.value = result.items
    pagination.itemCount = result.total
  } catch (error) {
    message.error(error instanceof Error ? error.message : '加载规则失败')
  } finally {
    loading.value = false
  }
}

function applySearch() {
  pagination.page = 1
  void load()
}

function openCreate() {
  editingRule.value = null
  showForm.value = true
}

function openEdit(rule: Rule) {
  editingRule.value = rule
  showForm.value = true
}

function onSaved() {
  pagination.page = 1
  void load()
}

async function toggleEnabled(rule: Rule, enabled: boolean) {
  togglingId.value = rule.id
  const previous = rule.enabled
  rule.enabled = enabled
  try {
    await api.rules.setEnabled(rule.id, enabled)
    message.success(enabled ? '规则已启用' : '规则已停用')
  } catch (error) {
    rule.enabled = previous
    message.error(error instanceof Error ? error.message : '切换状态失败')
  } finally {
    togglingId.value = null
  }
}

function displayHost(host: string): string {
  return host.includes(':') ? `[${host}]` : host
}

function targetSchemeLabel(scheme: RuleTargetScheme): string {
  return scheme === 'both' ? '兼容' : scheme.toUpperCase()
}

function upstreamSchemeLabel(scheme: RuleUpstreamScheme): string {
  return scheme === 'follow' ? '跟随' : scheme.toUpperCase()
}

function endpointNode(scheme: string, host: string, port: number) {
  return h('div', { style: 'display: flex; align-items: center; min-width: 0' }, [
    h(
      'span',
      {
        style:
          'flex: 0 0 auto; margin-right: 7px; font-size: 11px; font-weight: 650; letter-spacing: 0.02em; opacity: 0.62',
      },
      scheme,
    ),
    h(
      'span',
      {
        class: 'mono',
        style:
          'min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12.5px',
      },
      `${host}:${port}`,
    ),
  ])
}

const columns = computed<DataTableColumns<Rule>>(() => [
  {
    title: '规则',
    key: 'name',
    width: 210,
    render: (row) =>
      h('div', { style: 'font-weight: 600; font-size: 13px; line-height: 1.4' }, row.name),
  },
  {
    title: '访问地址',
    key: 'target_url',
    width: 260,
    render: (row) =>
      endpointNode(targetSchemeLabel(row.target_scheme), displayHost(row.match_host), row.match_port),
  },
  {
    title: '转发地址',
    key: 'upstream_url',
    width: 380,
    render: (row) =>
      endpointNode(
        upstreamSchemeLabel(row.upstream_scheme),
        displayHost(row.upstream_host),
        row.upstream_port,
      ),
  },
  {
    title: '状态',
    key: 'enabled',
    width: 80,
    render: (row) =>
      h(
        'div',
        {
          style: 'display: inline-flex; align-items: center',
          onClick: (event: MouseEvent) => event.stopPropagation(),
        },
        [
          h(NSwitch, {
            size: 'small',
            value: row.enabled,
            loading: togglingId.value === row.id,
            onUpdateValue: (value: boolean) => void toggleEnabled(row, value),
          }),
        ],
      ),
  },
])

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="规则管理">
      <n-button secondary :loading="loading" @click="load">
        <template #icon>
          <n-icon><RefreshOutline /></n-icon>
        </template>
        刷新
      </n-button>
      <n-button type="primary" @click="openCreate">
        <template #icon>
          <n-icon><AddOutline /></n-icon>
        </template>
        新增规则
      </n-button>
    </PageHeader>

    <n-card :bordered="false" class="soft-card">
      <div class="toolbar">
        <n-input
          v-model:value="keyword"
          class="search-input"
          placeholder="搜索规则名称或地址"
          clearable
          @keyup.enter="applySearch"
          @clear="applySearch"
        >
          <template #prefix>
            <n-icon :size="16"><SearchOutline /></n-icon>
          </template>
        </n-input>
        <n-button secondary @click="applySearch">搜索</n-button>
      </div>

      <n-data-table
        remote
        size="small"
        :columns="columns"
        :data="rules"
        :loading="loading"
        :pagination="pagination"
        :bordered="false"
        :single-line="false"
        :scroll-x="930"
        :row-key="rowKey"
        :row-props="rowProps"
        @update:page="onPageChange"
        @update:page-size="onPageSizeChange"
      >
        <template #empty>
          <n-empty description="还没有转发规则" style="padding: 38px 0">
            <template #extra>
              <n-button size="small" type="primary" @click="openCreate">新增规则</n-button>
            </template>
          </n-empty>
        </template>
      </n-data-table>
    </n-card>

    <RuleFormModal
      v-model:show="showForm"
      :rule="editingRule"
      @saved="onSaved"
      @deleted="onSaved"
    />
  </div>
</template>

<style scoped>
.soft-card {
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), inset 0 0 0 1px rgba(148, 163, 184, 0.1);
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}

.search-input {
  width: 340px;
  max-width: 100%;
}

@media (max-width: 720px) {
  .search-input {
    width: 100%;
  }
}
</style>
