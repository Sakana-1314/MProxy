<script setup lang="ts">
import { useRouter } from 'vue-router'
import { NButton, NIcon, useThemeVars } from 'naive-ui'
import { ArrowBackOutline, CompassOutline } from '@vicons/ionicons5'

import AppLogo from '@/components/AppLogo.vue'
import { siteName } from '@/lib/site'

const router = useRouter()
const tv = useThemeVars()
</script>

<template>
  <div class="not-found" :style="{ color: tv.textColor1 }">
    <div class="panel">
      <AppLogo :size="46" :radius="14" />
      <div class="code">404</div>
      <h1>页面走丢了</h1>
      <p :style="{ color: tv.textColor3 }">
        你访问的页面不存在，或已被移动到其他位置。{{ siteName }} 的管理入口仍然正常运行。
      </p>
      <div class="actions">
        <n-button type="primary" @click="router.replace('/dashboard')">
          <template #icon>
            <n-icon><CompassOutline /></n-icon>
          </template>
          回到概览
        </n-button>
        <n-button quaternary @click="router.back()">
          <template #icon>
            <n-icon><ArrowBackOutline /></n-icon>
          </template>
          返回上一页
        </n-button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.not-found {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 32px;
}

.panel {
  max-width: 420px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
}

.code {
  margin-top: 26px;
  font-size: 64px;
  line-height: 1;
  font-weight: 700;
  letter-spacing: -0.05em;
  background: linear-gradient(135deg, #6366f1, #8b5cf6);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

h1 {
  margin: 14px 0 8px;
  font-size: 24px;
  letter-spacing: -0.03em;
}

p {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
}

.actions {
  display: flex;
  gap: 10px;
  margin-top: 26px;
  flex-wrap: wrap;
  justify-content: center;
}
</style>
