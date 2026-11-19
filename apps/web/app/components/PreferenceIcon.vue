<script setup lang="ts">
defineProps<{ name: 'language' | 'system' | 'light' | 'dark' }>()
</script>

<template>
  <svg
    width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"
  >
    <g v-if="name === 'language'">
      <circle cx="12" cy="12" r="9" />
      <ellipse cx="12" cy="12" rx="4" ry="9" />
      <path d="M3 12h18" />
    </g>
    <g v-else-if="name === 'light'">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M19 5l-1.5 1.5m-11 11L5 19" />
    </g>
    <path v-else-if="name === 'dark'" d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5a9 9 0 1 0 11 11Z" />
    <g v-else>
      <rect x="3" y="4" width="18" height="13" rx="2" />
      <path d="M12 17v3m-4 0h8" />
    </g>
  </svg>
</template>
