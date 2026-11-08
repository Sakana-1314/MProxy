<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAlert,
  NButton,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  useThemeVars,
} from 'naive-ui'
import {
  GitNetworkOutline,
  LockClosedOutline,
  MoonOutline,
  PeopleOutline,
  PersonOutline,
  PulseOutline,
  ShieldCheckmarkOutline,
  SpeedometerOutline,
  SunnyOutline,
} from '@vicons/ionicons5'

import { api } from '@/api'
import AppLogo from '@/components/AppLogo.vue'
import { setSession } from '@/lib/auth'
import { siteName } from '@/lib/site'
import { isDark, toggleTheme } from '@/lib/theme'

const route = useRoute()
const router = useRouter()
const tv = useThemeVars()

const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')
const changed = route.query.changed === '1'

const features = [
  { icon: GitNetworkOutline, title: '精确放行', text: '只放行配置过的地址，其余一律拒绝' },
  { icon: SpeedometerOutline, title: '流量与配额', text: '按账号累计用量，配额永久有效' },
  { icon: PeopleOutline, title: '账号管理', text: '随时启停账号，可设置有效期' },
  { icon: PulseOutline, title: '用量概览', text: '请求与流量趋势一目了然' },
]

async function submit() {
  if (loading.value) {
    return
  }
  error.value = ''
  if (!username.value.trim() || !password.value) {
    error.value = '请输入管理员账号和密码'
    return
  }
  loading.value = true
  try {
    const result = await api.login(username.value.trim(), password.value)
    setSession(result.token, result.admin)
    const redirect = route.query.redirect
    const target =
      typeof redirect === 'string' && redirect.startsWith('/') && !redirect.startsWith('//')
        ? redirect
        : '/dashboard'
    await router.replace(target)
  } catch (err) {
    error.value = err instanceof Error ? err.message : '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page" :style="{ background: tv.bodyColor, color: tv.textColor1 }">
    <section class="brand-panel">
      <div class="orb orb-a" />
      <div class="orb orb-b" />

      <div class="brand-top">
        <AppLogo :size="44" :radius="13" />
        <div>
          <div class="brand-name">{{ siteName }}</div>
          <div class="brand-sub">MProxy</div>
        </div>
      </div>

      <div class="hero">
        <div class="eyebrow">ADMIN CONSOLE</div>
        <h1>统一管理转发规则<br />与代理账号</h1>
        <p class="hero-text">精确放行每一个地址，流量与配额清晰可控。</p>
      </div>

      <div class="feature-grid">
        <div v-for="item in features" :key="item.title" class="feature">
          <div class="feature-icon">
            <n-icon :size="18"><component :is="item.icon" /></n-icon>
          </div>
          <div>
            <div class="feature-title">{{ item.title }}</div>
            <div class="feature-text">{{ item.text }}</div>
          </div>
        </div>
      </div>

      <svg class="network-art" viewBox="0 0 560 220" fill="none" aria-hidden="true">
        <g stroke="rgba(255,255,255,0.25)" stroke-width="1.2">
          <path d="M24 168 C110 54 218 62 300 122 S458 196 536 76" />
          <path d="M24 120 C132 196 256 162 344 78 S474 24 536 132" />
          <path d="M24 58 C150 82 246 40 360 64 S480 128 536 108" opacity="0.55" />
        </g>
        <g fill="#fff">
          <circle cx="24" cy="168" r="4.2" />
          <circle cx="300" cy="122" r="5.2" />
          <circle cx="536" cy="76" r="4.2" />
          <circle cx="344" cy="78" r="3.4" opacity="0.75" />
          <circle cx="132" cy="196" r="2.6" opacity="0.6" />
          <circle cx="474" cy="128" r="2.6" opacity="0.6" />
        </g>
        <g fill="none" stroke="rgba(255,255,255,0.55)">
          <circle cx="300" cy="122" r="14" />
          <circle cx="300" cy="122" r="25" opacity="0.35" />
        </g>
      </svg>

      <div class="brand-foot">
        <n-icon :size="14"><ShieldCheckmarkOutline /></n-icon>
        仅用于你拥有或已获授权的服务
      </div>
    </section>

    <section class="form-panel">
      <div class="theme-switch">
        <n-button
          quaternary
          circle
          size="small"
          aria-label="切换主题"
          title="切换主题"
          @click="toggleTheme"
        >
          <template #icon>
            <n-icon :size="18">
              <SunnyOutline v-if="isDark" />
              <MoonOutline v-else />
            </n-icon>
          </template>
        </n-button>
      </div>

      <div class="form-wrap">
        <div class="mobile-brand">
          <AppLogo :size="40" :radius="12" />
          <div>
            <div class="brand-name">{{ siteName }}</div>
            <div class="brand-sub">MProxy</div>
          </div>
        </div>

        <div class="form-heading">
          <h2>欢迎回来</h2>
        </div>

        <n-alert v-if="changed" type="success" :bordered="false" style="margin-bottom: 14px">
          密码已修改，请使用新密码重新登录。
        </n-alert>
        <n-alert v-if="error" type="error" :bordered="false" style="margin-bottom: 14px">
          {{ error }}
        </n-alert>

        <n-form label-placement="top" :show-require-mark="false" @submit.prevent="submit">
          <n-form-item label="管理员账号">
            <n-input
              v-model:value="username"
              size="large"
              placeholder="请输入管理员账号"
              autofocus
              :input-props="{ autocomplete: 'username' }"
            >
              <template #prefix>
                <n-icon :size="17"><PersonOutline /></n-icon>
              </template>
            </n-input>
          </n-form-item>
          <n-form-item label="密码">
            <n-input
              v-model:value="password"
              size="large"
              type="password"
              show-password-on="click"
              placeholder="请输入密码"
              :input-props="{ autocomplete: 'current-password' }"
            >
              <template #prefix>
                <n-icon :size="17"><LockClosedOutline /></n-icon>
              </template>
            </n-input>
          </n-form-item>

          <n-button
            type="primary"
            size="large"
            block
            :loading="loading"
            class="login-button"
            attr-type="submit"
          >
            登录控制台
          </n-button>
        </n-form>
      </div>
    </section>
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  overflow: hidden;
}

.brand-panel {
  position: relative;
  flex: 1.12;
  min-width: 0;
  padding: 44px 56px;
  display: flex;
  flex-direction: column;
  background:
    radial-gradient(900px 520px at 78% 8%, rgba(129, 140, 248, 0.35), transparent 60%),
    radial-gradient(700px 480px at 8% 100%, rgba(14, 165, 233, 0.2), transparent 58%),
    linear-gradient(145deg, #17173a 0%, #272477 48%, #4c1d95 100%);
  color: #fff;
  overflow: hidden;
}

.orb {
  position: absolute;
  border-radius: 50%;
  filter: blur(10px);
  pointer-events: none;
}

.orb-a {
  width: 340px;
  height: 340px;
  right: -120px;
  top: -100px;
  background: radial-gradient(circle, rgba(139, 92, 246, 0.35), transparent 68%);
}

.orb-b {
  width: 420px;
  height: 420px;
  left: -160px;
  bottom: -220px;
  background: radial-gradient(circle, rgba(99, 102, 241, 0.3), transparent 68%);
}

.brand-top {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 13px;
}

.brand-name {
  font-size: 15px;
  font-weight: 650;
  letter-spacing: -0.01em;
}

.brand-sub {
  font-size: 10.5px;
  letter-spacing: 0.14em;
  opacity: 0.5;
  margin-top: 2px;
}

.hero {
  position: relative;
  z-index: 2;
  margin-top: auto;
  padding-top: 56px;
  max-width: 570px;
}

.eyebrow {
  font-size: 10.5px;
  letter-spacing: 0.18em;
  font-weight: 600;
  color: #c7d2fe;
  opacity: 0.85;
}

.hero h1 {
  margin: 16px 0 14px;
  font-size: clamp(30px, 3.4vw, 46px);
  line-height: 1.18;
  letter-spacing: -0.035em;
  font-weight: 680;
}

.hero-text {
  margin: 0;
  font-size: 14.5px;
  line-height: 1.75;
  color: rgba(226, 232, 240, 0.74);
  max-width: 500px;
}

.feature-grid {
  position: relative;
  z-index: 2;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px 26px;
  margin-top: 30px;
  max-width: 640px;
}

.feature {
  display: flex;
  gap: 11px;
  align-items: flex-start;
}

.feature-icon {
  width: 32px;
  height: 32px;
  border-radius: 9px;
  background: rgba(255, 255, 255, 0.1);
  border: 1px solid rgba(255, 255, 255, 0.12);
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  color: #c7d2fe;
}

.feature-title {
  font-size: 12.5px;
  font-weight: 600;
}

.feature-text {
  font-size: 11.5px;
  line-height: 1.55;
  color: rgba(226, 232, 240, 0.55);
  margin-top: 2px;
}

.network-art {
  position: absolute;
  z-index: 1;
  left: 0;
  right: 0;
  bottom: 62px;
  width: 100%;
  opacity: 0.55;
  pointer-events: none;
}

.brand-foot {
  position: relative;
  z-index: 2;
  margin-top: 28px;
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11px;
  color: rgba(226, 232, 240, 0.45);
}

.form-panel {
  position: relative;
  flex: 0.88;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px 32px;
}

.theme-switch {
  position: absolute;
  top: 22px;
  right: 24px;
}

.form-wrap {
  width: 100%;
  max-width: 376px;
  animation: rise 0.35s ease both;
}

@keyframes rise {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.mobile-brand {
  display: none;
  align-items: center;
  gap: 11px;
  margin-bottom: 34px;
}

.form-heading {
  margin-bottom: 26px;
}

.form-heading h2 {
  margin: 0;
  font-size: 27px;
  letter-spacing: -0.03em;
  font-weight: 680;
}

.login-button {
  margin-top: 8px;
  height: 44px;
  font-weight: 600;
}

@media (max-width: 980px) {
  .brand-panel {
    display: none;
  }

  .form-panel {
    flex: 1;
    padding: 72px 24px 40px;
  }

  .mobile-brand {
    display: flex;
  }

  .theme-switch {
    top: 16px;
    right: 16px;
  }
}

@media (max-width: 520px) {
  .form-wrap {
    max-width: 100%;
  }

  .form-heading h2 {
    font-size: 24px;
  }
}
</style>
