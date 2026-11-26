import type { components } from '@stepolonia/sdk'
import { computed, ref, type Ref } from 'vue'
import type { EditableDraft, EditorField, EditorMetadata, Scalar } from '~/utils/admin-content'
import { ownValue, savedFieldValue, type FieldEdit } from '~/utils/draft-edit-buffer'
import { suggestSlug, type SlugRule } from '~/utils/slug-suggestion'

type DraftFieldContext = {
  uiLanguage: string
  initialLanguage: string
  defaultLanguage: string
  slugRules: SlugRule[]
  languages: components['schemas']['Language'][]
  translate: (key: string) => string
}

const contentFieldOrder = ['title', 'slug', 'body']

function fieldOrder(key: string) {
  const position = contentFieldOrder.indexOf(key)
  return position >= 0 ? position : contentFieldOrder.length
}

function editKey(field: EditorField, locale: string) {
  return `${field.field_id}/${locale}`
}

export class DraftFields {
  readonly edits = ref(new Map<string, FieldEdit>())
  readonly invalid = ref(new Set<string>())
  readonly selectedLanguage = ref('')
  readonly sharedFields = computed(() => this.metadata.value?.fields.filter(field => field.scope === 'shared') ?? [])
  readonly localeFields = computed(() => (this.metadata.value?.fields.filter(field => field.scope === 'localized') ?? [])
    .sort((left, right) => fieldOrder(left.key) - fieldOrder(right.key)))
  readonly addressField = computed(() => this.localeFields.value.find(field => field.key === 'slug' && field.config.slug))
  readonly visibleLocaleFields = computed(() => this.localeFields.value.filter(field => field !== this.addressField.value))
  readonly hiddenLanguages = computed(() => Object.keys(this.draft.value?.locales ?? {}).filter(code => !this.activeLanguage(code)))
  readonly missingAddress = computed(() => {
    const source = this.draft.value
    const field = this.addressField.value
    const locale = this.selectedLanguage.value
    const title = this.localeFields.value.find(candidate => candidate.key === 'title')
    if (!source?.locales || source.version !== this.metadata.value?.version || !field?.editable || !title || !this.activeLanguage(locale)) return
    if (this.valueFor(field, locale) !== undefined) return
    const text = this.valueFor(title, locale)
    const value = typeof text === 'string' ? this.suggestAddress(field, locale, text) : ''
    return value ? { field, locale, value } : undefined
  })

  constructor(
    private readonly draft: Ref<EditableDraft | undefined>,
    private readonly metadata: Ref<EditorMetadata | undefined>,
    private readonly context: DraftFieldContext,
  ) {}

  activeLanguage(code: string) {
    return this.context.languages.some(language => language.code === code)
  }

  languageName(code: string) {
    return this.context.languages.find(language => language.code === code)?.native_name ?? code
  }

  selectExistingLanguage(source: EditableDraft) {
    if (this.activeLanguage(this.selectedLanguage.value) || ownValue(source.locales, this.selectedLanguage.value)) return
    const available = this.context.languages.map(language => language.code)
    const populated = new Set(Object.keys(source.locales).filter(code => {
      const locale = ownValue(source.locales, code)
      return locale && Object.keys(locale.values).length > 0
    }))
    const preferences = [this.context.uiLanguage, this.context.defaultLanguage, ...available, ...this.hiddenLanguages.value]
    this.selectedLanguage.value = [this.context.initialLanguage, ...preferences.filter(code => populated.has(code)), ...preferences]
      .find(code => this.activeLanguage(code) || ownValue(source.locales, code)) ?? ''
  }

  description(field: EditorField) {
    const labels = ownValue(field.display_labels, this.context.uiLanguage)
    const builtIn = field.scope === 'localized' && field.visibility === 'public'
    return {
      label: labels?.label || (builtIn && fieldOrder(field.key) < contentFieldOrder.length
        ? this.context.translate(`admin.content_field_${field.key}`)
        : field.key),
      help: labels?.help || this.fieldHelp(field, builtIn),
    }
  }

  private fieldHelp(field: EditorField, builtIn: boolean) {
    if (field.kind === 'relation') return this.context.translate('admin.content_relation_help')
    if (builtIn && field.key === 'slug' && field.config.slug) return this.context.translate('admin.content_field_slug_help')
    return undefined
  }

  valueFor(field: EditorField, locale: string) {
    const edit = this.edits.value.get(editKey(field, locale))
    if (edit) return edit.value
    return this.draft.value ? savedFieldValue(this.draft.value, field, locale) : undefined
  }

  private writeEdit(field: EditorField, locale: string, value: Scalar | undefined) {
    if (!this.draft.value || !field.editable) return
    const key = editKey(field, locale)
    if (value === savedFieldValue(this.draft.value, field, locale)) this.edits.value.delete(key)
    else this.edits.value.set(key, { field, locale, value })
    this.invalid.value.delete(field.field_id)
  }

  private suggestAddress(field: EditorField, locale: string, title: string) {
    return suggestSlug(title, locale, this.context.slugRules, field.config.max_length, field.config.min_length)
  }

  edit(field: EditorField, locale: string, value: Scalar | undefined) {
    this.writeEdit(field, locale, value)
    const slug = this.addressField.value
    if (!locale || !this.draft.value?.locales || !field.editable || field.key !== 'title' || !slug?.editable) return
    if (typeof value !== 'string' || !value.trim() || value === savedFieldValue(this.draft.value, field, locale)) {
      this.edits.value.delete(editKey(slug, locale))
      return
    }
    this.writeEdit(slug, locale, this.suggestAddress(slug, locale, value))
  }

  clearRelation(field: EditorField, locale: string) {
    if (this.draft.value && field.editable && field.kind === 'relation') {
      this.edits.value.set(editKey(field, locale), { field, locale, value: undefined })
    }
  }

  missingFields(fields: EditorField[], locale: string) {
    return fields.filter(field => field.required && this.valueFor(field, locale) === undefined)
  }
}
