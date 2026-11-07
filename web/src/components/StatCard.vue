<script setup lang="ts">
import type { Component } from 'vue'
import { NCard, NIcon } from 'naive-ui'
import { useThemeVars } from 'naive-ui'

defineProps<{
  label: string
  value: string
  hint?: string
  accent: string
  icon: Component
}>()

const tv = useThemeVars()
</script>

<template>
  <n-card :bordered="false" class="stat-card" size="small">
    <div class="stat-inner">
      <div class="stat-icon" :style="{ background: `${accent}1f`, color: accent }">
        <n-icon :size="22">
          <component :is="icon" />
        </n-icon>
      </div>
      <div class="stat-content">
        <div class="stat-label" :style="{ color: tv.textColor3 }">{{ label }}</div>
        <div class="stat-value" :style="{ color: tv.textColor1 }">{{ value }}</div>
        <div v-if="hint" class="stat-hint" :style="{ color: tv.textColor3 }">{{ hint }}</div>
      </div>
    </div>
  </n-card>
</template>

<style scoped>
.stat-card {
  overflow: hidden;
  position: relative;
}

.stat-card::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  pointer-events: none;
  box-shadow: inset 0 0 0 1px rgba(148, 163, 184, 0.1);
}

.stat-inner {
  display: flex;
  align-items: flex-start;
  gap: 15px;
  min-height: 76px;
}

.stat-icon {
  width: 46px;
  height: 46px;
  border-radius: 13px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
}

.stat-content {
  min-width: 0;
}

.stat-label {
  font-size: 12.5px;
  font-weight: 500;
  margin-top: 1px;
}

.stat-value {
  font-size: 25px;
  font-weight: 680;
  line-height: 1.25;
  letter-spacing: -0.03em;
  margin-top: 2px;
  font-variant-numeric: tabular-nums;
}

.stat-hint {
  font-size: 11.5px;
  margin-top: 3px;
}
</style>
