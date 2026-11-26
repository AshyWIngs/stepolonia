<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ svg?: string }>()
const image = computed(() => props.svg ? `data:image/svg+xml,${encodeURIComponent(props.svg)}` : undefined)
</script>

<template>
  <span class="language-flag" aria-hidden="true">
    <img v-if="image" :src="image" alt="" width="24" height="16">
    <PreferenceIcon v-else name="language" />
  </span>
</template>

<style scoped>
.language-flag {
  display: inline-grid;
  flex: 0 0 auto;
  place-items: center;
  inline-size: 24px;
  block-size: 20px;
}

.language-flag img {
  box-sizing: border-box;
  border: 1px solid var(--admin-border, var(--public-border));
  border-radius: 2px;
  object-fit: contain;
}
</style>
