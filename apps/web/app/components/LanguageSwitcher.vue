<script setup lang="ts">
import { ref, useId } from 'vue'
import type { components } from '@stepolonia/sdk'

type LanguageChoice = Pick<components['schemas']['LanguageDTO'], 'code' | 'native_name' | 'direction'> & { href?: string }

const props = defineProps<{
  label: string
  current: LanguageChoice
  choices: LanguageChoice[]
  busy?: boolean
}>()
const emit = defineEmits<{ select: [code: string] }>()

const disclosure = ref<HTMLDetailsElement>()
const summary = ref<HTMLElement>()
const labelId = useId()
const currentId = useId()

function close(returnFocus: boolean) {
  if (!disclosure.value?.open) return
  disclosure.value.open = false
  if (returnFocus) summary.value?.focus()
}

function handleEscape(event: KeyboardEvent) {
  if (!disclosure.value?.open) return
  event.preventDefault()
  event.stopPropagation()
  close(true)
}

function handleFocusout(event: FocusEvent) {
  if (event.relatedTarget instanceof Node && disclosure.value?.contains(event.relatedTarget)) return
  close(false)
}

function select(code: string, event: MouseEvent) {
  if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  emit('select', code)
  close(true)
}
</script>

<template>
  <div class="language-switcher">
    <span :id="labelId" class="language-switcher-label">{{ label }}</span>
    <details
      v-if="choices.some(choice => choice.code !== current.code)" ref="disclosure" class="language-switcher-disclosure" data-testid="language-switcher"
      :aria-busy="busy || undefined" @keydown.esc="handleEscape" @focusout="handleFocusout"
    >
      <summary ref="summary" :aria-labelledby="`${labelId} ${currentId}`" :data-current-language="current.code">
        <span :id="currentId" :lang="current.code" :dir="current.direction">{{ current.native_name }}</span>
      </summary>
      <ul class="language-switcher-options">
        <li v-for="choice in choices" :key="choice.code">
          <NuxtLink
            v-if="choice.href" :to="choice.href" :lang="choice.code" :hreflang="choice.code" :dir="choice.direction"
            :data-language="choice.code" :aria-current="choice.code === props.current.code ? 'page' : undefined"
            @click="select(choice.code, $event)"
          >{{ choice.native_name }}</NuxtLink>
          <button
            v-else type="button" :disabled="busy" :lang="choice.code" :dir="choice.direction"
            :data-language="choice.code" :aria-current="choice.code === props.current.code ? 'true' : undefined"
            @click="select(choice.code, $event)"
          >{{ choice.native_name }}</button>
        </li>
      </ul>
    </details>
    <span v-else class="language-switcher-current" :lang="current.code" :dir="current.direction" :data-current-language="current.code">
      {{ current.native_name }}
    </span>
  </div>
</template>

<style scoped>
.language-switcher {
  display: grid;
  gap: .25rem;
  min-inline-size: 0;
  max-inline-size: 100%;
  --language-surface: var(--admin-card, var(--public-background));
  --language-highlight: var(--admin-bg, var(--public-surface));
  --language-border: var(--admin-border, var(--public-border));
  --language-muted: var(--admin-muted, var(--public-muted));
  --language-focus: var(--admin-focus, var(--public-link));
}

.language-switcher-label {
  color: var(--language-muted);
  font-size: .875rem;
}

.language-switcher-disclosure {
  position: relative;
  min-inline-size: 0;
}

.language-switcher :is(summary, .language-switcher-current) {
  box-sizing: border-box;
  min-block-size: 44px;
  padding: .65rem .75rem;
  overflow-wrap: anywhere;
  font-size: .875rem;
  line-height: 1.4;
}

.language-switcher summary,
.language-switcher-options {
  border: 1px solid var(--language-border);
  border-radius: .45rem;
  background: var(--language-surface);
}

.language-switcher summary {
  cursor: pointer;
}

.language-switcher-options {
  position: absolute;
  z-index: 2;
  inset-inline-start: 0;
  box-sizing: border-box;
  inline-size: max(100%, 8.5rem);
  max-block-size: min(17rem, 45vh);
  overflow: auto;
  list-style: none;
  padding: .25rem;
  margin: .25rem 0 0;
}

.language-switcher-options :is(a, button) {
  display: flex;
  align-items: center;
  box-sizing: border-box;
  inline-size: 100%;
  min-block-size: 44px;
  border: 0;
  border-radius: .25rem;
  padding: .4rem .6rem;
  background: transparent;
  color: inherit;
  text-align: start;
  text-decoration: none;
  font: inherit;
  overflow-wrap: anywhere;
  cursor: pointer;
}

.language-switcher-options :is(a, button):hover {
  background: var(--language-highlight);
}

.language-switcher-options [aria-current] {
  font-weight: 700;
}

.language-switcher :is(summary, a, button):focus-visible {
  outline: 2px solid var(--language-focus);
  outline-offset: 2px;
}

.language-switcher-options button:disabled {
  opacity: .65;
  cursor: default;
}
</style>
