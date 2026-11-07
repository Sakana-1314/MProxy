import { computed, ref, watch } from 'vue'
import { darkTheme, type GlobalTheme, type GlobalThemeOverrides } from 'naive-ui'

const STORAGE_KEY = 'cdnp-theme'
const saved = localStorage.getItem(STORAGE_KEY)
const prefersDark =
  typeof window !== 'undefined' &&
  window.matchMedia &&
  window.matchMedia('(prefers-color-scheme: dark)').matches

export const isDark = ref(saved ? saved === 'dark' : prefersDark)

watch(
  isDark,
  (value) => {
    localStorage.setItem(STORAGE_KEY, value ? 'dark' : 'light')
    document.documentElement.style.colorScheme = value ? 'dark' : 'light'
  },
  { immediate: true },
)

export const naiveTheme = computed<GlobalTheme | null>(() => (isDark.value ? darkTheme : null))

export function toggleTheme() {
  isDark.value = !isDark.value
}

export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#6366f1',
    primaryColorHover: '#818cf8',
    primaryColorPressed: '#4f46e5',
    primaryColorSuppl: '#6366f1',
    infoColor: '#0ea5e9',
    successColor: '#10b981',
    warningColor: '#f59e0b',
    errorColor: '#ef4444',
    borderRadius: '10px',
    borderRadiusSmall: '8px',
    fontFamily:
      '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans SC", sans-serif',
    fontFamilyMono:
      'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: '14px',
    lineHeight: '1.6',
  },
  Card: {
    borderRadius: '16px',
    paddingMedium: '20px 22px',
    paddingLarge: '24px 26px',
  },
  Button: {
    borderRadiusMedium: '9px',
    borderRadiusSmall: '8px',
    fontWeight: '500',
  },
  Input: {
    borderRadius: '9px',
  },
  Select: {
    peers: {
      InternalSelection: {
        borderRadius: '9px',
      },
    },
  },
  DataTable: {
    thFontWeight: '600',
    borderColor: 'rgba(148, 163, 184, 0.14)',
  },
  Menu: {
    itemHeight: '42px',
    borderRadius: '10px',
  },
  Dialog: {
    borderRadius: '16px',
  },
  Modal: {
    borderRadius: '16px',
  },
  Dropdown: {
    borderRadius: '12px',
  },
  Tabs: {
    tabFontWeightActive: '600',
  },
  Tag: {
    borderRadius: '6px',
  },
  Progress: {
    railHeight: '7px',
  },
}
