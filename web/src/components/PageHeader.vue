<script setup lang="ts">
import { useThemeVars } from 'naive-ui'

defineProps<{
  title: string
}>()

const tv = useThemeVars()
</script>

<template>
  <div class="page-header">
    <div class="page-heading">
      <h1 :style="{ color: tv.textColor1 }">{{ title }}</h1>
    </div>
    <div class="page-actions">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}

.page-heading h1 {
  margin: 0;
  font-size: 24px;
  line-height: 1.3;
  font-weight: 650;
  letter-spacing: -0.02em;
}

.page-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

@media (max-width: 640px) {
  .page-heading h1 {
    font-size: 21px;
  }
}
</style>
