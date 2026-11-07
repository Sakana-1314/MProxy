<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAvatar,
  NButton,
  NDrawer,
  NDrawerContent,
  NDropdown,
  NIcon,
  NLayout,
  NLayoutHeader,
  NLayoutSider,
  NMenu,
  NTooltip,
  useDialog,
  useThemeVars,
} from 'naive-ui'
import type { DropdownOption, MenuOption } from 'naive-ui'
import {
  ChevronDownOutline,
  GitNetworkOutline,
  KeyOutline,
  LogOutOutline,
  MenuOutline,
  MoonOutline,
  PeopleOutline,
  SettingsOutline,
  SpeedometerOutline,
  SunnyOutline,
} from '@vicons/ionicons5'

import AppLogo from '@/components/AppLogo.vue'
import { auth, clearSession } from '@/lib/auth'
import { renderIcon } from '@/lib/icons'
import { siteName } from '@/lib/site'
import { isDark, toggleTheme } from '@/lib/theme'

const route = useRoute()
const router = useRouter()
const dialog = useDialog()
const tv = useThemeVars()

const collapsed = ref(false)
const mobileMenuOpen = ref(false)
const isMobile = ref(window.innerWidth < 920)
const scrollRef = ref<HTMLElement | null>(null)

const menuOptions: MenuOption[] = [
  { label: '概览', key: 'dashboard', icon: renderIcon(SpeedometerOutline) },
  { label: '规则管理', key: 'rules', icon: renderIcon(GitNetworkOutline) },
  { label: '用户与配额', key: 'users', icon: renderIcon(PeopleOutline) },
  { label: '系统设置', key: 'settings', icon: renderIcon(SettingsOutline) },
]

const activeKey = computed(() => String(route.name ?? 'dashboard'))
const pageTitle = computed(() => route.meta.title ?? '')

const userOptions: DropdownOption[] = [
  { label: '修改管理员密码', key: 'password', icon: renderIcon(KeyOutline) },
  { type: 'divider', key: 'divider' },
  { label: '退出登录', key: 'logout', icon: renderIcon(LogOutOutline) },
]

const avatarLetter = computed(() => (auth.admin?.username?.[0] ?? 'A').toUpperCase())

function onMenuSelect(key: string) {
  mobileMenuOpen.value = false
  if (key !== route.name) {
    void router.push({ name: key })
  }
}

function onUserSelect(key: string) {
  if (key === 'logout') {
    dialog.warning({
      title: '退出登录',
      content: '确定要退出管理后台吗？',
      positiveText: '退出',
      negativeText: '取消',
      onPositiveClick: () => {
        clearSession()
        void router.replace('/login')
      },
    })
    return
  }
  if (key === 'password') {
    void router.push({ path: '/settings', hash: '#password' })
  }
}

function updateViewport() {
  isMobile.value = window.innerWidth < 920
  if (!isMobile.value) {
    mobileMenuOpen.value = false
  }
}

onMounted(() => {
  window.addEventListener('resize', updateViewport)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', updateViewport)
})

watch(
  () => route.fullPath,
  () => {
    mobileMenuOpen.value = false
    scrollRef.value?.scrollTo({ top: 0, behavior: 'auto' })
  },
)
</script>

<template>
  <n-layout class="admin-layout" :style="{ background: tv.bodyColor }">
    <n-layout has-sider class="admin-layout">
      <n-layout-sider
        v-if="!isMobile"
        :collapsed="collapsed"
        :width="238"
        :collapsed-width="72"
        collapse-mode="width"
        :show-trigger="false"
        bordered
        :native-scrollbar="false"
        :style="{ background: tv.cardColor }"
      >
        <div class="sider-inner">
          <div class="brand" :class="{ collapsed }">
            <AppLogo :size="38" :radius="11" />
            <div v-if="!collapsed" class="brand-text">
              <div class="brand-name" :title="siteName">{{ siteName }}</div>
              <div class="brand-sub">MProxy</div>
            </div>
          </div>

          <n-menu
            class="side-menu"
            :value="activeKey"
            :options="menuOptions"
            :collapsed="collapsed"
            :collapsed-width="64"
            :collapsed-icon-size="20"
            :indent="18"
            @update:value="onMenuSelect"
          />

        </div>
      </n-layout-sider>

      <n-layout class="main-layout" :style="{ background: tv.bodyColor }">
        <n-layout-header
          class="app-header"
          :style="{ background: tv.cardColor, borderColor: tv.borderColor }"
        >
          <div class="header-left">
            <n-button
              quaternary
              circle
              size="small"
              class="menu-toggle"
              aria-label="展开或收起导航"
              title="展开或收起导航"
              @click="isMobile ? (mobileMenuOpen = true) : (collapsed = !collapsed)"
            >
              <template #icon>
                <n-icon :size="19"><MenuOutline /></n-icon>
              </template>
            </n-button>
            <div class="breadcrumb">
              <span class="crumb-root">控制台</span>
              <span class="crumb-sep">/</span>
              <span class="crumb-current" :style="{ color: tv.textColor1 }">{{ pageTitle }}</span>
            </div>
          </div>

          <div class="header-right">
            <n-tooltip trigger="hover">
              <template #trigger>
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
              </template>
              {{ isDark ? '切换到浅色模式' : '切换到深色模式' }}
            </n-tooltip>

            <n-dropdown :options="userOptions" trigger="click" placement="bottom-end" @select="onUserSelect">
              <button class="user-trigger" type="button">
                <n-avatar round :size="30" class="user-avatar">{{ avatarLetter }}</n-avatar>
                <span class="user-name">{{ auth.admin?.username ?? 'admin' }}</span>
                <n-icon :size="14" class="user-caret"><ChevronDownOutline /></n-icon>
              </button>
            </n-dropdown>
          </div>
        </n-layout-header>

        <div ref="scrollRef" class="content-scroll">
          <div class="page-wrap">
            <router-view v-slot="{ Component }">
              <transition name="page" mode="out-in">
                <component :is="Component" />
              </transition>
            </router-view>
          </div>
        </div>
      </n-layout>
    </n-layout>

    <n-drawer v-model:show="mobileMenuOpen" :width="276" placement="left">
      <n-drawer-content :native-scrollbar="false" closable body-content-style="padding: 0">
        <div class="drawer-inner">
          <div class="brand drawer-brand">
            <AppLogo :size="38" :radius="11" />
            <div class="brand-text">
              <div class="brand-name">{{ siteName }}</div>
              <div class="brand-sub">MProxy</div>
            </div>
          </div>
          <n-menu
            :value="activeKey"
            :options="menuOptions"
            :indent="18"
            @update:value="onMenuSelect"
          />
        </div>
      </n-drawer-content>
    </n-drawer>
  </n-layout>
</template>

<style scoped>
.admin-layout {
  height: 100vh;
}

.main-layout {
  height: 100vh;
  min-width: 0;
}

.sider-inner {
  display: flex;
  flex-direction: column;
  height: 100vh;
  padding: 14px 12px 12px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 4px 8px 16px;
  min-height: 58px;
  overflow: hidden;
}

.brand.collapsed {
  justify-content: center;
  padding-left: 0;
  padding-right: 0;
}

.brand-text {
  min-width: 0;
}

.brand-name {
  font-size: 14.5px;
  font-weight: 650;
  letter-spacing: -0.01em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.brand-sub {
  font-size: 10.5px;
  letter-spacing: 0.06em;
  opacity: 0.48;
  margin-top: 1px;
  white-space: nowrap;
}

.side-menu {
  flex: 1;
  margin-top: 4px;
}

.app-header {
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  border-bottom: 1px solid;
  gap: 16px;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  min-width: 0;
}

.crumb-root,
.crumb-sep {
  opacity: 0.45;
}

.crumb-current {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.user-trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  border: 0;
  background: transparent;
  padding: 3px 6px 3px 3px;
  border-radius: 10px;
  cursor: pointer;
  color: inherit;
  transition: background 0.18s ease;
}

.user-trigger:hover {
  background: rgba(148, 163, 184, 0.12);
}

.user-avatar {
  background: linear-gradient(135deg, #6366f1, #8b5cf6);
  color: #fff;
  font-weight: 650;
  font-size: 13px;
}

.user-name {
  font-size: 13px;
  font-weight: 500;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-caret {
  opacity: 0.45;
}

.content-scroll {
  height: calc(100vh - 64px);
  overflow-y: auto;
  padding: 24px;
}

.drawer-inner {
  display: flex;
  flex-direction: column;
  min-height: 100%;
  padding: 12px;
}

.drawer-brand {
  padding-bottom: 18px;
}

@media (max-width: 920px) {
  .content-scroll {
    padding: 18px 16px 28px;
  }

  .app-header {
    padding: 0 14px;
  }

  .user-name,
  .user-caret {
    display: none;
  }
}

@media (max-width: 520px) {
  .breadcrumb {
    font-size: 13px;
  }

  .content-scroll {
    padding: 16px 12px 24px;
  }
}
</style>
