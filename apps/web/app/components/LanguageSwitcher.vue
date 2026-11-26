<script setup lang="ts">
import type { components } from '@stepolonia/sdk'

type LanguageChoice = Pick<components['schemas']['LanguageDTO'], 'code' | 'native_name' | 'direction'> & { href?: string }

defineProps<{
  label: string
  current: LanguageChoice
  choices: LanguageChoice[]
  flags?: Record<string, string>
  staticLabelVisible?: boolean
  busy?: boolean
}>()
const emit = defineEmits<{ select: [code: string], follow: [code: string, href: string, keyboard: boolean] }>()

function plainActivation(event: MouseEvent) {
  return !event.defaultPrevented && event.button === 0 && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey
}

function followLink(code: string, href: string, event: MouseEvent, close: () => void) {
  if (!plainActivation(event)) return
  event.preventDefault()
  close()
  emit('follow', code, href, event.detail === 0)
}

function select(code: string, event: MouseEvent, close: () => void) {
  if (!plainActivation(event)) return
  emit('select', code)
  close()
}
</script>

<template>
  <HeaderPreference
    :label="label" :current="current.native_name" :current-language="current.code"
    :current-direction="current.direction" icon="language" test-id="language-switcher"
    :interactive="choices.some(choice => choice.code !== current.code)"
    :static-label-visible="staticLabelVisible" :busy="busy"
    :trigger-attrs="{ 'data-current-language': current.code }"
  >
    <template #icon>
      <LanguageFlag :svg="flags?.[current.code]" />
    </template>
    <template #default="{ close }">
      <li v-for="choice in choices" :key="choice.code">
        <NuxtLink v-if="choice.href" v-slot="{ href }" :to="choice.href" custom>
          <a
            :href="href ?? choice.href" :lang="choice.code" :hreflang="choice.code" :dir="choice.direction"
            :data-language="choice.code" :aria-current="choice.code === current.code ? 'page' : undefined"
            @click="followLink(choice.code, choice.href, $event, close)"
          >
            <LanguageFlag :svg="flags?.[choice.code]" />
            {{ choice.native_name }}
          </a>
        </NuxtLink>
        <button
          v-else type="button" :disabled="busy" :lang="choice.code" :dir="choice.direction"
          :data-language="choice.code" :aria-current="choice.code === current.code ? 'true' : undefined"
          @click="select(choice.code, $event, close)"
        >
          <LanguageFlag :svg="flags?.[choice.code]" />
          {{ choice.native_name }}
        </button>
      </li>
    </template>
  </HeaderPreference>
</template>
