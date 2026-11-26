import { computed, ref, type Ref } from 'vue'
import type { EditablePortal } from '~/utils/admin-site-settings'
import { normalizeSlugCharacter, type SlugRule } from '~/utils/slug-suggestion'

const slugRulesScope = 'slug-rules'

function slugRuleKey(rule: Pick<SlugRule, 'language_code' | 'character'>) {
  return JSON.stringify([rule.language_code, rule.character])
}

function changedSlugRules(saved: SlugRule[], next: SlugRule[]) {
  const previous = new Map(saved.map(rule => [slugRuleKey(rule), rule]))
  const upsert = next.filter((rule) => {
    const key = slugRuleKey(rule)
    const before = previous.get(key)
    previous.delete(key)
    return before?.replacement !== rule.replacement
  })
  const remove = [...previous.values()].map(({ language_code, character }) => ({ language_code, character }))
  return { upsert: upsert.length ? upsert : undefined, remove: remove.length ? remove : undefined }
}

export class PortalSlugRuleDraft {
  readonly rules = ref<SlugRule[]>([])
  readonly dirty = computed(() => JSON.stringify(this.rules.value) !== JSON.stringify(this.editableSettings.value?.slug_rules ?? []))

  constructor(
    private readonly editableSettings: Ref<EditablePortal | null>,
    private readonly localeLabel: (code: string) => string,
    private readonly removedValueLabel: () => string,
    private readonly recordSavedValues: (scope: string, values: string[]) => void,
  ) {}

  private normalizedRules() {
    return this.rules.value.map(rule => ({ ...rule, character: normalizeSlugCharacter(rule.character) }))
  }

  sync(editable: EditablePortal) {
    const previous = this.editableSettings.value
    if (previous && this.dirty.value) {
      editable.slug_rules_version = previous.slug_rules_version
      editable.slug_rules = previous.slug_rules
    } else {
      this.rules.value = editable.slug_rules
    }
  }

  input(editable: EditablePortal) {
    const delta = changedSlugRules(editable.slug_rules, this.normalizedRules())
    if (delta.upsert || delta.remove) return { expected_version: editable.slug_rules_version, ...delta }
    this.rules.value = editable.slug_rules
  }

  commit(editable: EditablePortal, version: string) {
    editable.slug_rules = this.normalizedRules()
    this.rules.value = editable.slug_rules
    editable.slug_rules_version = version
  }

  rebase(editable: EditablePortal, fresh: EditablePortal) {
    const { upsert, remove } = changedSlugRules(editable.slug_rules, this.normalizedRules())
    const merged = new Map(fresh.slug_rules.map(rule => [slugRuleKey(rule), rule]))
    const prior = new Map(editable.slug_rules.map(rule => [slugRuleKey(rule), rule]))
    this.recordSavedValues(slugRulesScope, [...(upsert ?? []), ...(remove ?? [])].flatMap((rule) => {
      const key = slugRuleKey(rule)
      const current = merged.get(key)
      return prior.get(key)?.replacement !== current?.replacement
        ? [`${this.localeLabel(rule.language_code)} · ${rule.character} → ${current?.replacement ?? this.removedValueLabel()}`] : []
    }))
    for (const key of remove ?? []) merged.delete(slugRuleKey(key))
    for (const rule of upsert ?? []) merged.set(slugRuleKey(rule), rule)
    this.rules.value = [...merged.values()]
    editable.slug_rules = fresh.slug_rules
    editable.slug_rules_version = fresh.slug_rules_version
  }
}
