<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useThemeVars } from 'naive-ui'

import type { SeriesPoint } from '@/api/types'
import { formatBytesShort, formatCount, formatDate, formatDateTime, formatHourLabel } from '@/lib/format'

const props = defineProps<{
  points: SeriesPoint[]
  range: '24h' | '7d' | '30d'
}>()

const tv = useThemeVars()
const shell = ref<HTMLElement | null>(null)
const chartWidth = ref(920)
const hoverIndex = ref<number | null>(null)

const height = 300
const pad = { top: 18, right: 52, bottom: 34, left: 62 }
const plotWidth = computed(() => Math.max(1, chartWidth.value - pad.left - pad.right))
const plotHeight = height - pad.top - pad.bottom
const count = computed(() => props.points.length)
const hasData = computed(() => props.points.some((point) => point.bytes > 0 || point.requests > 0))
const maxBytes = computed(() => Math.max(1, ...props.points.map((point) => point.bytes)))
const maxRequests = computed(() => Math.max(1, ...props.points.map((point) => point.requests)))

const gradientId = `traffic-gradient-${Math.random().toString(36).slice(2, 9)}`

let observer: ResizeObserver | null = null

onMounted(() => {
  if (!shell.value) {
    return
  }
  const update = () => {
    chartWidth.value = Math.max(320, shell.value?.clientWidth ?? 920)
  }
  update()
  observer = new ResizeObserver(update)
  observer.observe(shell.value)
})

onBeforeUnmount(() => {
  observer?.disconnect()
})

function xAt(index: number): number {
  if (count.value <= 1) {
    return pad.left + plotWidth.value / 2
  }
  return pad.left + (index / (count.value - 1)) * plotWidth.value
}

function yForBytes(value: number): number {
  return pad.top + plotHeight - (value / maxBytes.value) * plotHeight
}

function yForRequests(value: number): number {
  return pad.top + plotHeight - (value / maxRequests.value) * plotHeight
}

const linePath = computed(() =>
  props.points
    .map((point, index) => `${index === 0 ? 'M' : 'L'}${xAt(index).toFixed(2)},${yForBytes(point.bytes).toFixed(2)}`)
    .join(' '),
)

const areaPath = computed(() => {
  if (!props.points.length) {
    return ''
  }
  const baseline = pad.top + plotHeight
  const segments = props.points
    .map((point, index) => `L${xAt(index).toFixed(2)},${yForBytes(point.bytes).toFixed(2)}`)
    .join(' ')
  return `M${pad.left.toFixed(2)},${baseline.toFixed(2)} ${segments} L${xAt(count.value - 1).toFixed(2)},${baseline.toFixed(2)} Z`
})

const requestsPath = computed(() =>
  props.points
    .map(
      (point, index) =>
        `${index === 0 ? 'M' : 'L'}${xAt(index).toFixed(2)},${yForRequests(point.requests).toFixed(2)}`,
    )
    .join(' '),
)

const yTicks = computed(() =>
  Array.from({ length: 5 }, (_, index) => {
    const ratio = index / 4
    return {
      ratio,
      y: pad.top + plotHeight * (1 - ratio),
      label: formatBytesShort(maxBytes.value * ratio),
    }
  }),
)

const requestTicks = computed(() =>
  Array.from({ length: 3 }, (_, index) => {
    const ratio = index / 2
    return {
      ratio,
      y: pad.top + plotHeight * (1 - ratio),
      label: formatCount(maxRequests.value * ratio),
    }
  }),
)

const xLabels = computed(() => {
  if (!count.value) {
    return []
  }
  const labelCount =
    chartWidth.value < 560 ? 3 : chartWidth.value < 820 ? 4 : props.range === '24h' ? 6 : 5
  const seen = new Set<number>()
  const result: Array<{ index: number; x: number; label: string }> = []
  for (let slot = 0; slot < labelCount; slot += 1) {
    const index = Math.round((slot * (count.value - 1)) / Math.max(1, labelCount - 1))
    if (seen.has(index)) {
      continue
    }
    seen.add(index)
    const ts = props.points[index]?.ts ?? 0
    result.push({
      index,
      x: xAt(index),
      label: props.range === '24h' ? formatHourLabel(ts) : formatDate(ts).slice(5),
    })
  }
  return result
})

const hoverPoint = computed(() => {
  if (hoverIndex.value === null || !props.points[hoverIndex.value]) {
    return null
  }
  return props.points[hoverIndex.value]
})

const tooltipStyle = computed(() => {
  if (hoverIndex.value === null || !count.value) {
    return {}
  }
  const ratio = xAt(hoverIndex.value) / chartWidth.value
  const transform = ratio < 0.15 ? 'translateX(0)' : ratio > 0.85 ? 'translateX(-100%)' : 'translateX(-50%)'
  return {
    left: `${(xAt(hoverIndex.value) / chartWidth.value) * 100}%`,
    transform,
  }
})

const tooltipTime = computed(() => {
  if (!hoverPoint.value) {
    return ''
  }
  return props.range === '24h'
    ? `${formatDate(hoverPoint.value.ts)} ${formatHourLabel(hoverPoint.value.ts)}`
    : formatDateTime(hoverPoint.value.ts, false).slice(0, 10)
})

function onMove(event: MouseEvent) {
  if (!hasData.value || !shell.value || !count.value) {
    return
  }
  const rect = shell.value.getBoundingClientRect()
  const relativeX = event.clientX - rect.left
  const ratio = (relativeX - pad.left) / plotWidth.value
  const index = Math.round(ratio * Math.max(0, count.value - 1))
  hoverIndex.value = Math.max(0, Math.min(count.value - 1, index))
}
</script>

<template>
  <div
    ref="shell"
    class="chart-shell"
    @mousemove="onMove"
    @mouseleave="hoverIndex = null"
  >
    <div class="legend">
      <span class="legend-item">
        <i class="dot solid" />
        流量
      </span>
      <span class="legend-item">
        <i class="dot dashed" />
        请求数（右轴）
      </span>
    </div>

    <div class="chart-canvas">
      <svg
        :width="chartWidth"
        :height="height"
        :viewBox="`0 0 ${chartWidth} ${height}`"
        role="img"
        aria-label="代理流量趋势"
      >
        <defs>
          <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#6366f1" stop-opacity="0.3" />
            <stop offset="100%" stop-color="#6366f1" stop-opacity="0.015" />
          </linearGradient>
        </defs>

        <g>
          <line
            v-for="tick in yTicks"
            :key="`grid-${tick.ratio}`"
            :x1="pad.left"
            :x2="chartWidth - pad.right"
            :y1="tick.y"
            :y2="tick.y"
            :stroke="tv.dividerColor"
            stroke-width="1"
            stroke-dasharray="3 5"
            opacity="0.65"
          />
        </g>

        <g>
          <text
            v-for="tick in yTicks"
            :key="`y-${tick.ratio}`"
            :x="pad.left - 10"
            :y="tick.y + 4"
            text-anchor="end"
            :fill="tv.textColor3"
            font-size="11"
          >
            {{ tick.label }}
          </text>
          <text
            v-for="tick in requestTicks"
            :key="`r-${tick.ratio}`"
            :x="chartWidth - pad.right + 10"
            :y="tick.y + 4"
            text-anchor="start"
            :fill="tv.textColor3"
            font-size="11"
            opacity="0.7"
          >
            {{ tick.label }}
          </text>
        </g>

        <path v-if="hasData" :d="areaPath" :fill="`url(#${gradientId})`" />
        <path
          v-if="hasData"
          :d="linePath"
          fill="none"
          stroke="#6366f1"
          stroke-width="2.4"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
        <path
          v-if="hasData"
          :d="requestsPath"
          fill="none"
          stroke="#10b981"
          stroke-width="1.6"
          stroke-dasharray="5 5"
          stroke-linecap="round"
          opacity="0.85"
        />

        <g>
          <text
            v-for="item in xLabels"
            :key="`x-${item.index}`"
            :x="item.x"
            :y="height - 10"
            text-anchor="middle"
            :fill="tv.textColor3"
            font-size="11"
          >
            {{ item.label }}
          </text>
        </g>

        <g v-if="hoverPoint && hoverIndex !== null">
          <line
            :x1="xAt(hoverIndex)"
            :x2="xAt(hoverIndex)"
            :y1="pad.top"
            :y2="pad.top + plotHeight"
            stroke="#6366f1"
            stroke-width="1"
            stroke-dasharray="4 4"
            opacity="0.45"
          />
          <circle
            :cx="xAt(hoverIndex)"
            :cy="yForBytes(hoverPoint.bytes)"
            r="4.5"
            fill="#6366f1"
            stroke="#fff"
            stroke-width="2"
          />
          <circle
            :cx="xAt(hoverIndex)"
            :cy="yForRequests(hoverPoint.requests)"
            r="3.5"
            fill="#10b981"
            stroke="#fff"
            stroke-width="1.5"
          />
        </g>
      </svg>

      <div v-if="!hasData" class="empty-state" :style="{ color: tv.textColor3 }">
        当前区间暂无流量数据
      </div>

      <div
        v-if="hoverPoint"
        class="chart-tooltip"
        :style="{
          ...tooltipStyle,
          background: tv.cardColor,
          borderColor: tv.borderColor,
          color: tv.textColor1,
        }"
      >
        <div class="tooltip-time" :style="{ color: tv.textColor3 }">{{ tooltipTime }}</div>
        <div class="tooltip-row">
          <span>流量</span>
          <strong>{{ formatBytesShort(hoverPoint.bytes) }}</strong>
        </div>
        <div class="tooltip-row">
          <span>请求</span>
          <strong>{{ formatCount(hoverPoint.requests) }}</strong>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chart-shell {
  position: relative;
  width: 100%;
  padding-top: 2px;
}

.legend {
  display: flex;
  align-items: center;
  gap: 18px;
  margin-bottom: 6px;
  font-size: 12px;
  opacity: 0.75;
  flex-wrap: wrap;
}

.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 7px;
}

.dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  display: inline-block;
}

.dot.solid {
  background: #6366f1;
}

.dot.dashed {
  border: 1.5px dashed #10b981;
  background: transparent;
}

.chart-canvas {
  position: relative;
  width: 100%;
  overflow: hidden;
}

svg {
  display: block;
  max-width: 100%;
  overflow: visible;
}

.empty-state {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  pointer-events: none;
}

.chart-tooltip {
  position: absolute;
  top: 30px;
  min-width: 148px;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid;
  box-shadow: 0 12px 32px rgba(15, 23, 42, 0.14);
  pointer-events: none;
  z-index: 3;
  font-size: 12px;
  background: var(--n-color, #fff);
}

.tooltip-time {
  font-size: 11px;
  margin-bottom: 6px;
  font-variant-numeric: tabular-nums;
}

.tooltip-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  line-height: 1.7;
}

.tooltip-row strong {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

@media (max-width: 560px) {
  .chart-tooltip {
    top: 24px;
  }
}
</style>
