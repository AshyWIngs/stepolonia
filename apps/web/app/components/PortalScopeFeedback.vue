<script setup lang="ts">
defineProps<{
  scope: string
  status: string
  error: string
  reload: string
  review: string
  values: string[]
  disabled: boolean
}>()

defineEmits<{ reload: [] }>()
</script>

<template>
  <output v-if="status" data-testid="scoped-settings-status" :data-saved-scope="scope" tabindex="-1">{{ status }}</output>
  <p v-if="error" :id="scope === 'language-flags' ? 'scoped-settings-error' : undefined" role="alert">{{ error }}</p>
  <button v-if="reload" type="button" :data-reload-scope="scope" :disabled="disabled" @click="$emit('reload')">{{ reload }}</button>
  <details v-if="values.length" open>
    <summary>{{ review }}</summary>
    <ul>
      <li v-for="(value, index) in values" :key="index">{{ value }}</li>
    </ul>
  </details>
</template>
