<script setup lang="ts">
withDefaults(defineProps<{ size?: number; radius?: number }>(), {
  size: 36,
  radius: 10,
})
</script>

<template>
  <div
    class="app-logo"
    :style="{ width: `${size}px`, height: `${size}px`, borderRadius: `${radius}px` }"
  >
    <img src="/logo.png" alt="MProxy" />
  </div>
</template>

<style scoped>
.app-logo {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  overflow: hidden;
  background: #fff;
  box-shadow: 0 4px 14px rgba(37, 99, 235, 0.18);
}

img {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: cover;
}
</style>
