<script setup lang="ts">
import { computed } from 'vue'

const glyphs = {
  content: 'M6 3h8l4 4v14H6zM14 3v5h4M9 12h6m-6 4h6',
  registry: 'M12 3 3 8l9 5 9-5-9-5ZM3 12l9 5 9-5M3 16l9 5 9-5',
  settings: 'M4 6h5m4 0h7M4 12h10m4 0h2M4 18h3m4 0h9M9 4v4m5 2v4m-7 2v4',
  localization: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM3 12h18M12 3a13 13 0 0 1 0 18 13 13 0 0 1 0-18Z',
  access: 'M12 3 4 6v5c0 5 3 8 8 10 5-2 8-5 8-10V6zM9 12l2 2 4-4',
  identity: 'M4 6h16v12H4zM8 10h3m-3 4h3m4-4h2m-2 4h2',
  appearance: 'M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2zm0 4h16m-9 0v10',
  languages: 'M3 5h12m-6-2v2M5 8c1 4 4 7 8 9M13 5c-1 5-5 9-10 11m13 5 3-8 3 8m-5-3h4',
  translations: 'M4 4h16v12H9l-5 4zM8 8h8m-8 4h5',
  users: 'M15 8a3 3 0 1 1-6 0 3 3 0 0 1 6 0ZM5 20v-2a4 4 0 0 1 4-4h6a4 4 0 0 1 4 4v2M3 15a3 3 0 0 1 2-3m16 3a3 3 0 0 0-2-3',
  roles: 'M7 11h10v9H7zM9 11V8a3 3 0 0 1 6 0v3m-3 4v1',
  audit: 'M7 3h10v18H7zM10 7h4m-4 4h4m-4 4h4m-10-6h3m10 6h3',
  security: 'M12 3 5 6v5c0 5 3 8 7 10 4-2 7-5 7-10V6zm0 6v4m0 3h.01',
  rate_limits: 'M4 19a8 8 0 1 1 16 0zM12 15l4-4M7 15h.01M12 9h.01M17 15h.01',
  collection: 'M4 7h16v13H4zM5 4h14m-10 7h6',
  menu: 'M4 6h16M4 12h16M4 18h16',
  more: 'M5 12h14m-7-7v14',
  add: 'M12 5v14M5 12h14',
  save: 'M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h12l4 4v12a2 2 0 0 1-2 2ZM7 3v6h9M7 21v-8h10v8',
} as const

const props = defineProps<{ name: keyof typeof glyphs, iconSvg?: string }>()
const mask = computed(() => props.iconSvg ? `url("data:image/svg+xml;charset=utf-8,${encodeURIComponent(props.iconSvg)}")` : undefined)
</script>

<template>
  <span v-if="mask" class="admin-icon admin-icon--custom" :style="{ maskImage: mask }" aria-hidden="true" />
  <svg
    v-else class="admin-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"
  >
    <path :d="glyphs[name]" />
  </svg>
</template>

<style scoped>
.admin-icon {
  display: block;
  flex: none;
  inline-size: 20px;
  block-size: 20px;
}

.admin-icon--custom {
  background: currentColor;
  mask-size: contain;
  mask-position: center;
  mask-repeat: no-repeat;
}
</style>
