<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NEmpty,
  NIcon,
  NInput,
  NProgress,
  NSpace,
  NSwitch,
  NTag,
  useDialog,
  useMessage,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import type { Component } from 'vue'
import {
  AddCircleOutline,
  AddOutline,
  CreateOutline,
  RefreshOutline,
  SearchOutline,
  TrashOutline,
} from '@vicons/ionicons5'

import { api } from '@/api'
import type { ProxyUser } from '@/api/types'
import AddQuotaModal from '@/components/AddQuotaModal.vue'
import PageHeader from '@/components/PageHeader.vue'
import UserFormModal from '@/components/UserFormModal.vue'
import {
  expiryText,
  formatBytes,
  formatQuota,
  usagePercent,
} from '@/lib/format'

const message = useMessage()
const dialog = useDialog()

const loading = ref(false)
const users = ref<ProxyUser[]>([])
const keyword = ref('')
const showForm = ref(false)
const editingUser = ref<ProxyUser | null>(null)
const showQuota = ref(false)
const quotaUser = ref<ProxyUser | null>(null)
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

function rowKey(row: ProxyUser) {
  return row.id
}

async function load() {
  loading.value = true
  try {
    const result = await api.users.list({
      page: pagination.page,
      page_size: pagination.pageSize,
      keyword: keyword.value.trim(),
    })
    users.value = result.items
    pagination.itemCount = result.total
  } catch (error) {
    message.error(error instanceof Error ? error.message : '加载用户失败')
  } finally {
    loading.value = false
  }
}

function applySearch() {
  pagination.page = 1
  void load()
}

function openCreate() {
  editingUser.value = null
  showForm.value = true
}

function openEdit(user: ProxyUser) {
  editingUser.value = user
  showForm.value = true
}

function openQuota(user: ProxyUser) {
  quotaUser.value = user
  showQuota.value = true
}

function onSaved() {
  pagination.page = 1
  void load()
}

function onQuotaSaved() {
  void load()
}

async function toggleEnabled(user: ProxyUser, enabled: boolean) {
  const previous = user.enabled
  user.enabled = enabled
  togglingId.value = user.id
  try {
    await api.users.setEnabled(user.id, enabled)
    message.success(enabled ? '账号已启用' : '账号已停用')
  } catch (error) {
    user.enabled = previous
    message.error(error instanceof Error ? error.message : '切换状态失败')
  } finally {
    togglingId.value = null
  }
}

function confirmDelete(user: ProxyUser) {
  dialog.error({
    title: '删除代理用户',
    content: `确定删除「${user.username}」吗？该账号将立即无法认证。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await api.users.remove(user.id)
        message.success('用户已删除')
        if (users.value.length === 1 && pagination.page > 1) {
          pagination.page -= 1
        }
        await load()
      } catch (error) {
        message.error(error instanceof Error ? error.message : '删除失败')
      }
    },
  })
}

function progressStatus(percent: number): 'default' | 'success' | 'warning' | 'error' {
  if (percent >= 95) return 'error'
  if (percent >= 80) return 'warning'
  if (percent > 0) return 'success'
  return 'default'
}

function actionButton(
  label: string,
  icon: Component,
  handler: () => void,
  type: 'default' | 'primary' | 'error' = 'default',
) {
  return h(
    NButton,
    { text: true, size: 'small', type, onClick: handler },
    {
      icon: () => h(NIcon, null, { default: () => h(icon) }),
      default: () => label,
    },
  )
}

const columns = computed<DataTableColumns<ProxyUser>>(() => [
  {
    title: '用户',
    key: 'username',
    width: 180,
    render: (row) =>
      h('div', null, [
        h('div', { style: 'font-weight: 600; font-size: 13px' }, row.username),
        h(
          'div',
          {
            style:
              'font-size: 11.5px; opacity: .5; margin-top: 2px; max-width: 165px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap',
            title: row.note,
          },
          row.note || '—',
        ),
      ]),
  },
  {
    title: '状态',
    key: 'enabled',
    width: 80,
    render: (row) =>
      h(NSwitch, {
        size: 'small',
        value: row.enabled,
        loading: togglingId.value === row.id,
        onUpdateValue: (value: boolean) => void toggleEnabled(row, value),
      }),
  },
  {
    title: '累计流量 / 永久配额',
    key: 'total_bytes',
    width: 300,
    render: (row) => {
      const used = row.total_bytes
      const quota = row.quota_bytes
      const unlimited = quota <= 0
      const percent = usagePercent(used, quota)
      const status = progressStatus(percent)
      return h('div', { style: 'min-width: 250px' }, [
        h(
          'div',
          {
            style:
              'display: flex; align-items: baseline; justify-content: space-between; gap: 10px; font-size: 12px',
          },
          [
            h('span', { class: 'num' }, formatBytes(used)),
            h(
              'span',
              { class: 'num', style: 'opacity: .55; font-size: 11px' },
              unlimited ? '不限配额' : `/ ${formatQuota(quota)}`,
            ),
          ],
        ),
        unlimited
          ? null
          : h(NProgress, {
              type: 'line',
              percentage: Number(percent.toFixed(1)),
              status,
              showIndicator: false,
              height: 6,
              style: 'margin-top: 6px',
            }),
      ])
    },
  },
  {
    title: '账号有效期',
    key: 'expires_at',
    width: 120,
    render: (row) => {
      if (!row.expires_at) {
        return h('span', { style: 'font-size: 12px; opacity: .65' }, '永久')
      }
      const expired = row.expires_at * 1000 < Date.now()
      return h(
        NTag,
        { size: 'small', bordered: false, type: expired ? 'error' : 'default' },
        { default: () => expiryText(row.expires_at) },
      )
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 240,
    fixed: 'right',
    render: (row) =>
      h(NSpace, { size: 4, wrap: false }, { default: () => [
        actionButton('编辑', CreateOutline, () => openEdit(row)),
        actionButton('增加配额', AddCircleOutline, () => openQuota(row)),
        actionButton('删除', TrashOutline, () => confirmDelete(row), 'error'),
      ] }),
  },
])

onMounted(load)
</script>

<template>
  <div>
    <PageHeader title="用户与配额">
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
        新增用户
      </n-button>
    </PageHeader>

    <n-card :bordered="false" class="soft-card">
      <div class="toolbar">
        <div class="toolbar-left">
          <n-input
            v-model:value="keyword"
            class="search-input"
            placeholder="搜索用户名或备注"
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
      </div>

      <n-data-table
        remote
        size="small"
        :columns="columns"
        :data="users"
        :loading="loading"
        :pagination="pagination"
        :bordered="false"
        :single-line="false"
        :scroll-x="920"
        :row-key="rowKey"
        @update:page="onPageChange"
        @update:page-size="onPageSizeChange"
      >
        <template #empty>
          <n-empty description="还没有代理用户" style="padding: 38px 0">
            <template #extra>
              <n-button size="small" type="primary" @click="openCreate">创建第一个用户</n-button>
            </template>
          </n-empty>
        </template>
      </n-data-table>
    </n-card>

    <UserFormModal v-model:show="showForm" :user="editingUser" @saved="onSaved" />
    <AddQuotaModal v-model:show="showQuota" :user="quotaUser" @saved="onQuotaSaved" />
  </div>
</template>

<style scoped>
.soft-card {
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), inset 0 0 0 1px rgba(148, 163, 184, 0.1);
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.search-input {
  width: 300px;
  max-width: 100%;
}

@media (max-width: 720px) {
  .search-input,
  .toolbar-left {
    width: 100%;
  }
}
</style>
