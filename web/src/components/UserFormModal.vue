<script setup lang="ts">
import { nextTick, reactive, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NDatePicker,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NInputNumber,
  NModal,
  NSpace,
  NSwitch,
  useMessage,
} from 'naive-ui'
import type { FormInst, FormItemRule, FormRules } from 'naive-ui'
import { CopyOutline, RefreshOutline } from '@vicons/ionicons5'

import { api } from '@/api'
import type { ProxyUser, UserInput } from '@/api/types'
import { endOfDaySeconds, formatQuota } from '@/lib/format'

const props = defineProps<{
  show: boolean
  user: ProxyUser | null
}>()

const emit = defineEmits<{
  (event: 'update:show', value: boolean): void
  (event: 'saved'): void
}>()

interface UserFormState {
  username: string
  password: string
  enabled: boolean
  quotaGB: number | null
  expiresAt: number | null
  note: string
}

const message = useMessage()
const formRef = ref<FormInst | null>(null)
const saving = ref(false)
const form = reactive<UserFormState>(createDefaults())
const isEdit = ref(false)

function createDefaults(): UserFormState {
  return {
    username: '',
    password: '',
    enabled: true,
    quotaGB: 0,
    expiresAt: null,
    note: '',
  }
}

function hydrate() {
  isEdit.value = Boolean(props.user)
  if (props.user) {
    form.username = props.user.username
    form.password = ''
    form.enabled = props.user.enabled
    form.quotaGB = props.user.quota_bytes > 0 ? Number((props.user.quota_bytes / 1024 ** 3).toFixed(2)) : 0
    form.expiresAt = props.user.expires_at ? props.user.expires_at * 1000 : null
    form.note = props.user.note
  } else {
    Object.assign(form, createDefaults())
  }
  void nextTick(() => formRef.value?.restoreValidation())
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      hydrate()
    }
  },
)

const usernameRule: FormItemRule = {
  trigger: ['input', 'blur'],
  validator: (_rule, value: string) => {
    if (!value || !value.trim()) {
      return new Error('请输入用户名')
    }
    if (!/^[A-Za-z0-9._-]{2,32}$/.test(value.trim())) {
      return new Error('用户名需为 2-32 位字母、数字、点、下划线或短横线')
    }
    return true
  },
}

const passwordRule: FormItemRule = {
  trigger: ['input', 'blur'],
  validator: (_rule, value: string) => {
    if (!isEdit.value && !value) {
      return new Error('请输入代理密码')
    }
    if (value && (value.length < 8 || value.length > 128)) {
      return new Error('密码长度需为 8-128 个字符')
    }
    return true
  },
}

const formRules: FormRules = {
  username: usernameRule,
  password: passwordRule,
  quotaGB: { type: 'number', min: 0, trigger: ['blur', 'change'] },
}

function generatePassword() {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const values = new Uint32Array(16)
  crypto.getRandomValues(values)
  form.password = Array.from(values, (value) => alphabet[value % alphabet.length]).join('')
}

async function copyPassword() {
  if (!form.password) {
    return
  }
  try {
    await navigator.clipboard.writeText(form.password)
    message.success('密码已复制')
  } catch {
    message.warning('复制失败，请手动选择密码')
  }
}

async function save() {
  if (saving.value) {
    return
  }
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  const quotaBytes = Math.max(0, Math.round(Number(form.quotaGB ?? 0) * 1024 ** 3))
  const payload: UserInput = {
    username: form.username.trim(),
    enabled: form.enabled,
    expires_at: form.expiresAt ? endOfDaySeconds(form.expiresAt) : null,
    note: form.note.trim(),
  }
  if (!props.user) {
    payload.quota_bytes = quotaBytes
  }
  if (form.password) {
    payload.password = form.password
  }
  saving.value = true
  try {
    if (props.user) {
      await api.users.update(props.user.id, payload)
      message.success('用户已更新')
    } else {
      await api.users.create(payload)
      message.success('用户已创建')
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
    :title="user ? '编辑代理用户' : '新增代理用户'"
    style="width: 640px; max-width: calc(100vw - 32px)"
    @update:show="emit('update:show', $event)"
  >
    <n-form ref="formRef" :model="form" :rules="formRules" label-placement="top">
      <div class="form-grid">
        <n-form-item label="用户名" path="username" class="span-2">
          <n-input
            v-model:value="form.username"
            :disabled="isEdit"
            placeholder="2-32 位字母、数字、点、下划线或短横线"
            maxlength="32"
          />
        </n-form-item>

        <n-form-item :label="isEdit ? '代理密码（留空则不修改）' : '代理密码'" path="password" class="span-2">
          <n-input
            v-model:value="form.password"
            type="password"
            show-password-on="click"
            :placeholder="isEdit ? '留空保持当前密码' : '至少 8 个字符'"
            maxlength="128"
          >
            <template #suffix>
              <div class="password-actions">
                <n-button text type="primary" size="tiny" @click="generatePassword">
                  <template #icon>
                    <n-icon><RefreshOutline /></n-icon>
                  </template>
                  随机
                </n-button>
                <n-button
                  v-if="form.password"
                  text
                  size="tiny"
                  @click="copyPassword"
                >
                  <template #icon>
                    <n-icon><CopyOutline /></n-icon>
                  </template>
                  复制
                </n-button>
              </div>
            </template>
          </n-input>
        </n-form-item>

        <n-form-item v-if="!isEdit" label="初始配额（GB，0 为不限）" path="quotaGB">
          <n-input-number
            v-model:value="form.quotaGB"
            :min="0"
            :precision="2"
            style="width: 100%"
          >
            <template #suffix>GB</template>
          </n-input-number>
        </n-form-item>

        <n-form-item label="账号有效期">
          <n-date-picker
            v-model:value="form.expiresAt"
            type="date"
            clearable
            placeholder="永久有效"
            style="width: 100%"
          />
        </n-form-item>

        <n-alert v-if="isEdit" type="default" :bordered="false" class="span-2 quota-note">
          当前永久配额：{{ formatQuota(user?.quota_bytes ?? 0) }}。配额只能增加，不能减少或重置。
        </n-alert>

        <n-form-item label="备注" class="span-2">
          <n-input
            v-model:value="form.note"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 3 }"
            maxlength="200"
            show-count
            placeholder=""
          />
        </n-form-item>

        <n-form-item label="启用账号" class="span-2">
          <div class="switch-line">
            <n-switch v-model:value="form.enabled" />
          </div>
        </n-form-item>
      </div>
    </n-form>

    <template #footer>
      <div class="modal-footer">
        <n-space>
          <n-button @click="emit('update:show', false)">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">
            {{ user ? '保存修改' : '创建用户' }}
          </n-button>
        </n-space>
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 18px;
}

.span-2 {
  grid-column: span 2;
}

.password-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.quota-note {
  margin-bottom: 18px;
}

.switch-line {
  display: flex;
  align-items: center;
  gap: 10px;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
}

@media (max-width: 640px) {
  .form-grid {
    grid-template-columns: 1fr;
  }

  .span-2 {
    grid-column: span 1;
  }
}
</style>
