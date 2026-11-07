<script setup lang="ts">
import { watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  NConfigProvider,
  NDialogProvider,
  NGlobalStyle,
  NLoadingBarProvider,
  NMessageProvider,
  NNotificationProvider,
  dateZhCN,
  zhCN,
} from 'naive-ui'

import { siteName } from '@/lib/site'
import { naiveTheme, themeOverrides } from '@/lib/theme'

const route = useRoute()

watch(
  () => route.meta.title,
  () => {
    const title = route.meta.title
    document.title = title ? `${title} · ${siteName}` : siteName
  },
  { immediate: true },
)
</script>

<template>
  <n-config-provider
    :theme="naiveTheme"
    :theme-overrides="themeOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <n-loading-bar-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <n-message-provider>
            <n-global-style />
            <router-view />
          </n-message-provider>
        </n-notification-provider>
      </n-dialog-provider>
    </n-loading-bar-provider>
  </n-config-provider>
</template>
