<script setup lang="ts">
import { ref, useId } from 'vue'

defineProps<{
  label: string
  current: string
  currentLanguage?: string
  currentDirection?: 'ltr' | 'rtl' | 'auto'
  icon: 'language' | 'system' | 'light' | 'dark'
  testId: string
  triggerAttrs?: Record<string, string>
  interactive?: boolean
  staticLabelVisible?: boolean
  busy?: boolean
}>()

const details = ref<HTMLDetailsElement>()
const summary = ref<HTMLElement>()
const labelId = useId()
const currentId = useId()

function close(returnFocus = true) {
  if (!details.value?.open) return
  details.value.open = false
  if (returnFocus) summary.value?.focus()
}

function handleEscape(event: KeyboardEvent) {
  if (!details.value?.open) return
  event.preventDefault()
  event.stopPropagation()
  close()
}

function focusout(event: FocusEvent) {
  if (!(event.relatedTarget instanceof Node && details.value?.contains(event.relatedTarget))) close(false)
}
</script>

<template>
  <div class="header-preference">
    <details
      v-if="interactive" ref="details" :data-testid="testId"
      :aria-busy="busy || undefined" @keydown.esc="handleEscape" @focusout="focusout"
    >
      <summary
        ref="summary" class="header-preference-trigger"
        :aria-labelledby="`${labelId} ${currentId}`" :title="`${label} · ${current}`" v-bind="triggerAttrs"
      >
        <span :id="labelId" class="header-preference-label">{{ label }}</span>
        <span :id="currentId" class="header-preference-label" :lang="currentLanguage" :dir="currentDirection">{{ current }}</span>
        <PreferenceIcon :name="icon" />
      </summary>
      <ul class="header-preference-options">
        <slot :close="close" />
      </ul>
    </details>
    <span
      v-else class="header-preference-trigger" :aria-hidden="staticLabelVisible || undefined"
      :title="staticLabelVisible ? undefined : `${label} · ${current}`" v-bind="triggerAttrs"
    >
      <PreferenceIcon :name="icon" />
      <span v-if="!staticLabelVisible" class="header-preference-label">
        {{ label }} · <span :lang="currentLanguage" :dir="currentDirection">{{ current }}</span>
      </span>
    </span>
  </div>
</template>

<style scoped>
.header-preference {
  --preference-surface: var(--admin-card, var(--public-background));
  --preference-hover: var(--admin-bg, var(--public-surface));
  --preference-border: var(--admin-border, var(--public-border));
  --preference-focus: var(--admin-focus, var(--public-link));
  position: relative;
  inline-size: 44px;
  block-size: 44px;
}

.header-preference-label {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

.header-preference-trigger,
.header-preference-options {
  box-sizing: border-box;
  border: 1px solid var(--preference-border);
  border-radius: .45rem;
  background: var(--preference-surface);
  color: inherit;
}

.header-preference-trigger {
  display: grid;
  place-items: center;
  inline-size: 100%;
  block-size: 44px;
  list-style: none;
}

summary.header-preference-trigger { cursor: pointer; }
summary.header-preference-trigger::-webkit-details-marker { display: none; }
summary.header-preference-trigger:hover { background: var(--preference-hover); }
span.header-preference-trigger { border-color: transparent; background: transparent; }

.header-preference-options {
  position: absolute;
  z-index: 3;
  inset-block-start: calc(100% + .25rem);
  inset-inline-end: 0;
  inline-size: min(12rem, calc(100vw - 2rem));
  max-block-size: min(18rem, 45vh);
  overflow: auto;
  padding: .25rem;
  margin: 0;
  list-style: none;
}

.header-preference-options :deep(:is(a, button)) {
  display: flex;
  gap: .5rem;
  align-items: center;
  box-sizing: border-box;
  inline-size: 100%;
  min-block-size: 44px;
  padding: .45rem .5rem;
  border: 0;
  border-radius: .25rem;
  background: transparent;
  color: inherit;
  text-align: start;
  text-decoration: none;
  font: inherit;
  overflow-wrap: anywhere;
  cursor: pointer;
}

.header-preference-options :deep(:is(a, button):hover) { background: var(--preference-hover); }
.header-preference-options :deep(button:disabled) { opacity: .65; cursor: default; }
.header-preference-options :deep([aria-current]) { font-weight: 700; }
.header-preference-options :deep([aria-current]::after) {
  content: '';
  flex: 0 0 auto;
  inline-size: .35rem;
  block-size: .6rem;
  margin-inline-start: auto;
  border: solid currentColor;
  border-width: 0 2px 2px 0;
  transform: rotate(45deg);
}

.header-preference-trigger:focus-visible,
.header-preference-options :deep(:focus-visible) {
  outline: 2px solid var(--preference-focus);
  outline-offset: -3px;
}
</style>
