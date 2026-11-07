<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NCard, NForm, NFormItem, NIcon, NInput, useMessage } from 'naive-ui'
import { CopyOutline, KeyOutline, ServerOutline } from '@vicons/ionicons5'

import { api } from '@/api'
import type { AppSettings } from '@/api/types'
import PageHeader from '@/components/PageHeader.vue'
import { clearSession } from '@/lib/auth'

const message = useMessage()
const router = useRouter()
const route = useRoute()

const loading = ref(false)
const savingPassword = ref(false)
const settings = ref<AppSettings | null>(null)

const passwordForm = reactive({
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
})

const proxyDisplay = computed(() => {
  const listen = settings.value?.proxy_listen?.trim() ?? ''
  if (!listen) {
    return window.location.host
  }
  if (listen.startsWith(':')) {
    return `${window.location.hostname}${listen}`
  }
  if (listen.startsWith('0.0.0.0:')) {
    return `${window.location.hostname}:${listen.slice('0.0.0.0:'.length)}`
  }
  if (listen.startsWith('[::]:')) {
    return `${window.location.hostname}:${listen.slice('[::]:'.length)}`
  }
  return listen
})

async function loadSettings() {
  loading.value = true
  try {
    settings.value = await api.settings.get()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '加载设置失败')
  } finally {
    loading.value = false
  }
}

async function copyText(value: string, success = '已复制') {
  try {
    await navigator.clipboard.writeText(value)
    message.success(success)
  } catch {
    message.warning('复制失败，请手动选择复制')
  }
}

async function changePassword() {
  if (savingPassword.value) {
    return
  }
  if (!passwordForm.oldPassword || !passwordForm.newPassword) {
    message.warning('请填写原密码和新密码')
    return
  }
  if (passwordForm.newPassword.length < 8) {
    message.warning('新密码至少需要 8 个字符')
    return
  }
  if (passwordForm.newPassword !== passwordForm.confirmPassword) {
    message.warning('两次输入的新密码不一致')
    return
  }
  if (passwordForm.newPassword === passwordForm.oldPassword) {
    message.warning('新密码不能与原密码相同')
    return
  }
  savingPassword.value = true
  try {
    await api.changePassword(passwordForm.oldPassword, passwordForm.newPassword)
    clearSession()
    await router.replace({ path: '/login', query: { changed: '1' } })
  } catch (error) {
    message.error(error instanceof Error ? error.message : '修改密码失败')
  } finally {
    savingPassword.value = false
  }
}

onMounted(() => {
  void loadSettings()
  if (route.hash === '#password') {
    window.setTimeout(() => {
      document.getElementById('password')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 250)
  }
})
</script>

<template>
  <div>
    <PageHeader title="系统设置" />

    <n-spin :show="loading">
      <div class="settings-grid">
        <div class="column">
          <n-card id="password" :bordered="false" class="soft-card password-card">
            <template #header>
              <div class="card-head">
                <div class="card-title">管理员密码</div>
                <n-icon :size="20" style="opacity: 0.35"><KeyOutline /></n-icon>
              </div>
            </template>

            <n-form label-placement="top">
              <n-form-item label="原密码">
                <n-input
                  v-model:value="passwordForm.oldPassword"
                  type="password"
                  show-password-on="click"
                  placeholder="当前管理员密码"
                />
              </n-form-item>
              <n-form-item label="新密码">
                <n-input
                  v-model:value="passwordForm.newPassword"
                  type="password"
                  show-password-on="click"
                  placeholder="至少 8 个字符"
                />
              </n-form-item>
              <n-form-item label="确认新密码">
                <n-input
                  v-model:value="passwordForm.confirmPassword"
                  type="password"
                  show-password-on="click"
                  placeholder="再次输入新密码"
                />
              </n-form-item>
              <div class="form-actions">
                <n-button type="primary" :loading="savingPassword" @click="changePassword">
                  更新密码
                </n-button>
              </div>
            </n-form>
          </n-card>
        </div>

        <div class="column">
          <n-card :bordered="false" class="soft-card">
            <template #header>
              <div class="card-head">
                <div class="card-title">代理接入</div>
                <n-icon :size="20" style="opacity: 0.35"><ServerOutline /></n-icon>
              </div>
            </template>

            <div class="info-row">
              <span class="info-label">代理地址</span>
              <div class="info-value">
                <span class="mono">{{ proxyDisplay }}</span>
                <n-button text size="tiny" @click="copyText(proxyDisplay, '代理地址已复制')">
                  <template #icon>
                    <n-icon><CopyOutline /></n-icon>
                  </template>
                  复制
                </n-button>
              </div>
            </div>
          </n-card>
        </div>
      </div>
    </n-spin>
  </div>
</template>

<style scoped>
.settings-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.05fr) minmax(0, 0.95fr);
  gap: 16px;
  align-items: start;
}

.column {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}

.soft-card {
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), inset 0 0 0 1px rgba(148, 163, 184, 0.1);
}

.password-card {
  scroll-margin-top: 84px;
}

.card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.card-title {
  font-size: 15px;
  font-weight: 650;
  letter-spacing: -0.01em;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 2px;
}

.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  min-height: 30px;
}

.info-label {
  font-size: 12.5px;
  opacity: 0.6;
  flex: 0 0 auto;
}

.info-value {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 12.5px;
  min-width: 0;
  justify-content: flex-end;
  text-align: right;
  word-break: break-all;
}

@media (max-width: 1000px) {
  .settings-grid {
    grid-template-columns: 1fr;
  }
}
</style>
