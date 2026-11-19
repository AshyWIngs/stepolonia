<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { PortalLanguage } from '~/utils/admin-site-settings'
import { invalidSlugRuleIndex, maxSlugRules, normalizeSlugCharacter, type SlugRule } from '~/utils/slug-suggestion'

const props = defineProps<{
  rules: SlugRule[]
  languages: PortalLanguage[]
  defaultLanguage: string
  translate: (key: string) => string
}>()
const emit = defineEmits<{ 'update:rules': [rules: SlugRule[]] }>()

const editor = ref<HTMLElement>()
const addButton = ref<HTMLButtonElement>()
const language = ref(props.defaultLanguage)
const query = ref('')
const focused = ref(-1)
const submittedInvalid = ref(-1)
const choices = computed(() => {
  const options = new Map(props.languages.map(item => [item.code, item]))
  for (const { language_code: code } of props.rules) {
    if (!options.has(code)) options.set(code, { code, native_name: code, name: code, direction: 'ltr' })
  }
  return [...options.values()]
})
const direction = computed(() => props.languages.find(item => item.code === language.value)?.direction ?? 'auto')
const visible = computed(() => {
  const term = query.value.trim().toLowerCase()
  return props.rules.map((rule, index) => ({ rule, index })).filter(({ rule, index }) =>
    rule.language_code === language.value && (index === focused.value || !term ||
      [rule.character, rule.replacement].some(value => value.toLowerCase().includes(term))))
})
const duplicateIndexes = computed(() => {
  const first = new Map<string, number>()
  const duplicate = new Set<number>()
  props.rules.forEach((rule, index) => {
    const character = normalizeSlugCharacter(rule.character)
    if (!character) return
    const key = `${rule.language_code}\u0000${character}`
    const existing = first.get(key)
    if (existing === undefined) first.set(key, index)
    else {
      duplicate.add(existing)
      duplicate.add(index)
    }
  })
  return duplicate
})

watch(choices, (options) => {
  if (!options.some(option => option.code === language.value)) {
    language.value = options.find(option => option.code === props.defaultLanguage)?.code ?? options[0]?.code ?? ''
  }
}, { immediate: true })

function characterInvalid(rule: SlugRule, index: number) {
  return duplicateIndexes.value.has(index) || (submittedInvalid.value === index &&
    invalidSlugRuleIndex([{ ...rule, replacement: '' }]) === 0)
}

function replacementInvalid(rule: SlugRule, index: number) {
  return submittedInvalid.value === index && invalidSlugRuleIndex([rule]) === 0 &&
    invalidSlugRuleIndex([{ ...rule, replacement: '' }]) < 0
}

function focusRule(index: number, part: 'character' | 'replacement' = 'character') {
  editor.value?.querySelector<HTMLInputElement>(`#portal-slug-${part}-${index}`)?.focus()
}

async function addRule() {
  if (!language.value || props.rules.length >= maxSlugRules) return
  query.value = ''
  const index = props.rules.length
  emit('update:rules', [...props.rules, { language_code: language.value, character: '', replacement: '' }])
  await nextTick()
  focusRule(index)
}

function updateRule(index: number, part: 'character' | 'replacement', event: Event) {
  if (!(event.target instanceof HTMLInputElement)) return
  const value = event.target.value
  if (props.rules[index]?.[part] === value) return
  emit('update:rules', props.rules.map((rule, position) => position === index ? { ...rule, [part]: value } : rule))
}

async function removeRule(index: number) {
  focused.value = -1
  submittedInvalid.value = -1
  emit('update:rules', props.rules.filter((_, position) => position !== index))
  await nextTick()
  addButton.value?.focus()
}

function leaveRow(event: FocusEvent) {
  if (!(event.currentTarget instanceof HTMLElement) || !(event.relatedTarget instanceof Node) ||
    !event.currentTarget.contains(event.relatedTarget)) focused.value = -1
}

async function showInvalid(index: number) {
  const rule = props.rules[index]
  if (!rule) return
  language.value = rule.language_code
  query.value = ''
  submittedInvalid.value = index
  await nextTick()
  focusRule(index, characterInvalid(rule, index) ? 'character' : 'replacement')
}

defineExpose({ showInvalid })
</script>

<template>
  <section ref="editor" class="slug-rules" aria-labelledby="portal-slug-title">
    <h3 id="portal-slug-title">{{ translate('admin.slug_rules_title') }}</h3>
    <p id="portal-slug-help">{{ translate('admin.slug_rules_help') }}</p>
    <div class="slug-controls">
      <div>
        <label for="portal-slug-language">{{ translate('admin.slug_rules_language') }}</label>
        <select id="portal-slug-language" v-model="language" @change="focused = -1">
          <option v-for="item in choices" :key="item.code" :value="item.code" :lang="item.code" :dir="item.direction">
            {{ item.native_name }}
          </option>
        </select>
      </div>
      <div>
        <label for="portal-slug-search">{{ translate('admin.slug_rules_search') }}</label>
        <input id="portal-slug-search" v-model="query" type="search" @keydown.enter.prevent>
      </div>
      <button ref="addButton" type="button" :disabled="!language || rules.length >= maxSlugRules" @click="addRule">
        {{ translate('admin.slug_rules_add') }}
      </button>
    </div>
    <ul v-if="visible.length" class="slug-rules-list">
      <li
        v-for="{ rule, index } in visible" :key="index" class="slug-controls slug-rule"
        @focusin="focused = index" @focusout="leaveRow"
      >
        <div>
          <label :for="`portal-slug-character-${index}`">{{ translate('admin.slug_rules_character') }}</label>
          <input
            :id="`portal-slug-character-${index}`" :value="rule.character" :lang="language" :dir="direction"
            :aria-invalid="characterInvalid(rule, index) || undefined"
            aria-describedby="portal-slug-help" @input="updateRule(index, 'character', $event)"
          >
        </div>
        <div>
          <label :for="`portal-slug-replacement-${index}`">{{ translate('admin.slug_rules_replacement') }}</label>
          <input
            :id="`portal-slug-replacement-${index}`" :value="rule.replacement" dir="ltr" maxlength="16"
            :aria-invalid="replacementInvalid(rule, index) || undefined"
            aria-describedby="portal-slug-help" @input="updateRule(index, 'replacement', $event)"
          >
        </div>
        <button type="button" @click="removeRule(index)">{{ translate('admin.slug_rules_remove') }}</button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.slug-rules, .slug-rules-list, .slug-controls, .slug-controls > div {
  display: grid;
  gap: .5rem;
  min-inline-size: 0;
}

.slug-rules :is(h3, p), .slug-rules-list { margin: 0; }
.slug-rules p { color: var(--admin-muted); }
.slug-rules-list { list-style: none; padding: 0; }
.slug-controls { align-items: end; }
.slug-controls button { justify-self: start; white-space: normal; overflow-wrap: anywhere; }
.slug-rule { padding-block-start: .5rem; border-block-start: 1px solid var(--admin-border); }

.slug-controls button {
  min-block-size: 44px;
  max-inline-size: 100%;
  padding: .5rem .75rem;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  background: var(--admin-card);
  color: var(--admin-text);
  font: inherit;
}

.slug-controls [aria-invalid='true'] { border-color: var(--admin-error); }
.slug-rules :focus-visible { outline: 3px solid var(--admin-focus); outline-offset: 3px; }

@media (min-width: 60rem) {
  .slug-controls { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto; }
}
</style>
