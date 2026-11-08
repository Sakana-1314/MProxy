<script setup lang="ts">
import { nextTick, reactive, ref, watch } from 'vue'
import {
  NButton,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  useDialog,
  useMessage,
} from 'naive-ui'
import type { FormInst, FormItemRule, FormRules } from 'naive-ui'

import { api } from '@/api'
import type {
  Rule,
  RuleInput,
  RuleTargetScheme,
  RuleUpstreamScheme,
} from '@/api/types'

const props = defineProps<{
  show: boolean
  rule: Rule | null
}>()

const emit = defineEmits<{
  (event: 'update:show', value: boolean): void
  (event: 'saved'): void
  (event: 'deleted'): void
}>()

interface RuleFormState {
  name: string
  target_scheme: RuleTargetScheme
  match_host: string
  match_port: number | null
  upstream_scheme: RuleUpstreamScheme
  upstream_host: string
  upstream_port: number | null
  enabled: boolean
  remark: string
}

const targetSchemeOptions: Array<{ label: string; value: RuleTargetScheme }> = [
  { label: 'HTTP', value: 'http' },
  { label: 'HTTPS', value: 'https' },
  { label: '兼容', value: 'both' },
]

const upstreamSchemeOptions: Array<{ label: string; value: RuleUpstreamScheme }> = [
  { label: 'HTTP', value: 'http' },
  { label: 'HTTPS', value: 'https' },
  { label: '跟随', value: 'follow' },
]

const message = useMessage()
const dialog = useDialog()
const formRef = ref<FormInst | null>(null)
const saving = ref(false)
const deleting = ref(false)
const form = reactive<RuleFormState>(createDefaults())
let hydrating = false

function createDefaults(): RuleFormState {
  return {
    name: '',
    target_scheme: 'https',
    match_host: '',
    match_port: 443,
    upstream_scheme: 'https',
    upstream_host: '',
    upstream_port: 443,
    enabled: true,
    remark: '',
  }
}

function hydrate() {
  const source = props.rule
  const value: RuleFormState = source
    ? {
        name: source.name,
        target_scheme: source.target_scheme,
        match_host: source.match_host,
        match_port: source.match_port,
        upstream_scheme: source.upstream_scheme,
        upstream_host: source.upstream_host,
        upstream_port: source.upstream_port,
        enabled: source.enabled,
        remark: source.remark,
      }
    : createDefaults()
  hydrating = true
  Object.assign(form, value)
  void nextTick(() => {
    hydrating = false
    formRef.value?.restoreValidation()
  })
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      hydrate()
    }
  },
)

function defaultPort(scheme: RuleTargetScheme | RuleUpstreamScheme): number {
  if (scheme === 'http') {
    return 80
  }
  if (scheme === 'follow') {
    return form.target_scheme === 'http' ? 80 : 443
  }
  return 443
}

watch(
  () => form.target_scheme,
  (scheme, previous) => {
    if (hydrating) {
      return
    }
    if (form.match_port === defaultPort(previous) || form.match_port === null) {
      form.match_port = defaultPort(scheme)
    }
    if (form.upstream_scheme === 'follow') {
      const previousUpstreamDefault = previous === 'http' ? 80 : 443
      if (form.upstream_port === previousUpstreamDefault || form.upstream_port === null) {
        form.upstream_port = defaultPort(scheme)
      }
    }
  },
)

watch(
  () => form.upstream_scheme,
  (scheme, previous) => {
    if (hydrating) {
      return
    }
    const previousDefault = defaultPort(previous)
    if (form.upstream_port === previousDefault || form.upstream_port === null) {
      form.upstream_port = defaultPort(scheme)
    }
  },
)

function hasProtocolOrPath(value: string): boolean {
  return /[\s/?#]/.test(value) || value.includes('://')
}

const targetHostRule: FormItemRule = {
  trigger: ['input', 'blur'],
  validator: (_rule, value: string) => {
    if (!value || !value.trim()) {
      return new Error('请输入目标域名')
    }
    if (value.includes('*')) {
      return new Error('不支持通配符')
    }
    if (hasProtocolOrPath(value)) {
      return new Error('请填写域名')
    }
    return true
  },
}

const upstreamHostRule: FormItemRule = {
  trigger: ['input', 'blur'],
  validator: (_rule, value: string) => {
    if (!value || !value.trim()) {
      return new Error('请输入转发域名')
    }
    if (value.includes('*')) {
      return new Error('不支持通配符')
    }
    if (hasProtocolOrPath(value)) {
      return new Error('请填写域名')
    }
    return true
  },
}

const formRules: FormRules = {
  name: { required: true, message: '请输入规则名称', trigger: ['input', 'blur'] },
  match_host: targetHostRule,
  upstream_host: upstreamHostRule,
}

function validPort(value: number | null): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= 65535
}

function confirmDelete() {
  const rule = props.rule
  if (!rule || deleting.value) {
    return
  }
  dialog.error({
    title: '删除规则',
    content: `确定删除「${rule.name}」吗？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      deleting.value = true
      try {
        await api.rules.remove(rule.id)
        message.success('规则已删除')
        emit('deleted')
        emit('update:show', false)
      } catch (error) {
        message.error(error instanceof Error ? error.message : '删除失败')
      } finally {
        deleting.value = false
      }
    },
  })
}

async function save() {
  if (saving.value || deleting.value) {
    return
  }
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  if (!validPort(form.match_port)) {
    message.warning('请输入目标端口')
    return
  }
  if (!validPort(form.upstream_port)) {
    message.warning('请输入转发端口')
    return
  }
  const payload: RuleInput = {
    name: form.name.trim(),
    target_scheme: form.target_scheme,
    match_host: form.match_host.trim(),
    match_port: form.match_port,
    upstream_scheme: form.upstream_scheme,
    upstream_host: form.upstream_host.trim(),
    upstream_port: form.upstream_port,
    enabled: form.enabled,
    remark: form.remark.trim(),
  }
  saving.value = true
  try {
    if (props.rule) {
      await api.rules.update(props.rule.id, payload)
      message.success('规则已更新')
    } else {
      await api.rules.create(payload)
      message.success('规则已创建')
    }
    emit('saved')
    emit('update:show', false)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-modal
    :show="show"
    :mask-closable="false"
    preset="card"
    :title="rule ? '编辑转发规则' : '新增转发规则'"
    style="width: 700px; max-width: calc(100vw - 32px)"
    @update:show="emit('update:show', $event)"
  >
    <n-form
      ref="formRef"
      :model="form"
      :rules="formRules"
      label-placement="top"
      require-mark-placement="right-hanging"
    >
      <n-form-item label="规则名称" path="name">
        <n-input v-model:value="form.name" placeholder="" maxlength="64" />
      </n-form-item>

      <n-form-item label="访问地址" path="match_host">
        <div class="url-row">
          <n-select
            v-model:value="form.target_scheme"
            :options="targetSchemeOptions"
            class="scheme-select"
            aria-label="访问协议"
          />
          <n-input
            v-model:value="form.match_host"
            placeholder=""
            class="host-input"
            aria-label="目标域名"
          />
          <n-input-number
            v-model:value="form.match_port"
            :min="1"
            :max="65535"
            :precision="0"
            :show-button="false"
            class="port-input"
            aria-label="目标端口"
          />
        </div>
      </n-form-item>

      <n-form-item label="转发地址" path="upstream_host">
        <div class="url-row">
          <n-select
            v-model:value="form.upstream_scheme"
            :options="upstreamSchemeOptions"
            class="scheme-select"
            aria-label="转发协议"
          />
          <n-input
            v-model:value="form.upstream_host"
            placeholder=""
            class="host-input"
            aria-label="转发域名"
          />
          <n-input-number
            v-model:value="form.upstream_port"
            :min="1"
            :max="65535"
            :precision="0"
            :show-button="false"
            class="port-input"
            aria-label="转发端口"
          />
        </div>
      </n-form-item>

      <n-form-item label="备注">
        <n-input
          v-model:value="form.remark"
          type="textarea"
          :autosize="{ minRows: 2, maxRows: 3 }"
          maxlength="500"
          placeholder=""
        />
      </n-form-item>

      <n-form-item label="启用规则">
        <n-switch v-model:value="form.enabled" />
      </n-form-item>
    </n-form>

    <template #footer>
      <div class="modal-footer">
        <n-button
          v-if="rule"
          type="error"
          secondary
          :loading="deleting"
          :disabled="saving"
          @click="confirmDelete"
        >
          删除
        </n-button>
        <span v-else />
        <n-button type="primary" :loading="saving" :disabled="deleting" @click="save">
          保存
        </n-button>
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.url-row {
  display: grid;
  grid-template-columns: 108px minmax(0, 1fr) 92px;
  gap: 8px;
  width: 100%;
}

.scheme-select,
.host-input,
.port-input {
  width: 100%;
}

.modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

@media (max-width: 520px) {
  .url-row {
    grid-template-columns: 92px minmax(0, 1fr) 82px;
    gap: 6px;
  }
}
</style>
