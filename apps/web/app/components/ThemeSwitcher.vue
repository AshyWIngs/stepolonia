<script setup lang="ts">
import { themeModes, type ThemeMode } from '~/composables/usePublicTheme'

defineProps<{ translate: (key: string) => string }>()
const theme = defineModel<ThemeMode>({ required: true })
</script>

<template>
  <HeaderPreference
    :label="translate('theme.label')" :current="translate(`theme.${theme}`)" :icon="theme"
    test-id="theme-switcher" :trigger-attrs="{ 'data-current-theme': theme }" interactive
  >
    <template #default="{ close }">
      <li v-for="mode in themeModes" :key="mode">
        <button
          type="button" :data-theme="mode" :aria-current="mode === theme ? 'true' : undefined"
          @click="theme = mode; close()"
        >
          <PreferenceIcon :name="mode" />
          {{ translate(`theme.${mode}`) }}
        </button>
      </li>
    </template>
  </HeaderPreference>
</template>
