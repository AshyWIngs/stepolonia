<script setup lang="ts">
import type { components } from '@stepolonia/sdk'

type LanguageChoice = Pick<components['schemas']['LanguageDTO'], 'code' | 'native_name' | 'direction'> & { href?: string }

defineProps<{
  label: string
  current: LanguageChoice
  choices: LanguageChoice[]
  busy?: boolean
}>()
const emit = defineEmits<{ select: [code: string] }>()

function select(code: string, event: MouseEvent, close: () => void) {
  if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  emit('select', code)
  close()
}
</script>

<template>
  <HeaderPreference
    :label="label" :current="current.native_name" :current-language="current.code"
    :current-direction="current.direction" icon="language" test-id="language-switcher"
    :interactive="choices.some(choice => choice.code !== current.code)" :busy="busy"
    :trigger-attrs="{ 'data-current-language': current.code }"
  >
    <template #default="{ close }">
      <li v-for="choice in choices" :key="choice.code">
        <NuxtLink
          v-if="choice.href" :to="choice.href" :lang="choice.code" :hreflang="choice.code" :dir="choice.direction"
          :data-language="choice.code" :aria-current="choice.code === current.code ? 'page' : undefined"
          @click="select(choice.code, $event, close)"
        >{{ choice.native_name }}</NuxtLink>
        <button
          v-else type="button" :disabled="busy" :lang="choice.code" :dir="choice.direction"
          :data-language="choice.code" :aria-current="choice.code === current.code ? 'true' : undefined"
          @click="select(choice.code, $event, close)"
        >{{ choice.native_name }}</button>
      </li>
    </template>
  </HeaderPreference>
</template>
