<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NForm,
  NFormItem,
  NInputNumber,
  NModal,
  NSpace,
  useMessage,
} from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'

import { api } from '@/api'
import type { ProxyUser } from '@/api/types'
import { formatBytes, formatQuota } from '@/lib/format'

const props = defineProps<{
  show: boolean
  user: ProxyUser | null
}>()

const emit = defineEmits<{
  (event: 'update:show', value: boolean): void
  (event: 'saved'): void
}>()

const message = useMessage()
const formRef = ref<FormInst | null>(null)
const saving = ref(false)
const form = reactive<{ addGB: number | null }>({ addGB: 1 })

const unlimited = computed(() => (props.user?.quota_bytes ?? 0) <= 0)

const addedBytes = computed(() => Math.max(0, Math.round(Number(form.addGB ?? 0) * 1024 ** 3)))

const newQuota = computed(() => {
  const current = props.user?.quota_bytes ?? 0
  return current > 0 ? current + addedBytes.value : 0
})

const formRules: FormRules = {
  addGB: {
    type: 'number',
    required: true,
    validator: (_rule, value: number | null) => {
      if (unlimited.value) {
        return new Error('不限流量账号无需增加配额')
      }
      if (!value || value <= 0) {
        return new Error('请输入大于 0 的配额')
      }
      return true
    },
    trigger: ['blur', 'change'],
  },
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      form.addGB = 1
      saving.value = false
    }
  },
)

async function save() {
  if (!props.user || unlimited.value || saving.value) {
    return
  }
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  if (addedBytes.value <= 0) {
    message.warning('请输入大于 0 的配额')
    return
  }
  saving.value = true
  try {
    await api.users.addQuota(props.user.id, addedBytes.value)
    message.success('配额已增加，永久有效')
    emit('saved')
    emit('update:show', false)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '增加配额失败')
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
    title="增加永久配额"
    style="width: 520px; max-width: calc(100vw - 32px)"
    @update:show="emit('update:show', $event)"
  >
    <n-alert type="info" :bordered="false" style="margin-bottom: 18px">
      配额只能增加，不能减少或重置；增加后立即永久生效。累计流量超过配额后，新的代理连接会被拒绝。
    </n-alert>

    <n-alert v-if="user" type="default" :bordered="false" style="margin-bottom: 18px">
      账号 <strong>{{ user.username }}</strong> 当前已用
      <strong>{{ formatBytes(user.total_bytes) }}</strong>，永久配额
      <strong>{{ formatQuota(user.quota_bytes) }}</strong>。
    </n-alert>

    <n-alert v-if="unlimited" type="warning" :bordered="false" style="margin-bottom: 18px">
      该账号当前为不限流量，不能再增加有限配额（否则相当于降低额度）。
    </n-alert>

    <n-form ref="formRef" :model="form" :rules="formRules" label-placement="top">
      <n-form-item label="增加配额（GB）" path="addGB">
        <n-input-number
          v-model:value="form.addGB"
          :min="0.01"
          :precision="2"
          :disabled="unlimited"
          style="width: 100%"
        >
          <template #suffix>GB</template>
        </n-input-number>
        <template #feedback>
          <span class="field-hint">
            {{ unlimited ? '不限流量账号无需增加配额' : `增加后永久配额：${formatQuota(newQuota)}` }}
          </span>
        </template>
      </n-form-item>
    </n-form>

    <template #footer>
      <div class="modal-footer">
        <n-space>
          <n-button @click="emit('update:show', false)">取消</n-button>
          <n-button
            type="primary"
            :loading="saving"
            :disabled="unlimited"
            @click="save"
          >
            确认增加
          </n-button>
        </n-space>
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.field-hint {
  font-size: 11.5px;
  opacity: 0.62;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
}
</style>
