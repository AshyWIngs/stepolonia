import type { components } from '@stepolonia/sdk'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useAdminUI } from '~/composables/useAdminUI'
import { usePublicApi } from '~/composables/usePublicApi'
import type { RegistryField } from '~/utils/admin-content'
import { AdminContentError } from '~/utils/admin-request'
import { ownValue } from '~/utils/draft-edit-buffer'

type PublishedLanguage = components['schemas']['Language']
export type TranslationLanguage = Pick<PublishedLanguage, 'code' | 'native_name'> & Partial<Pick<PublishedLanguage, 'direction'>>
export type DisplayPart = 'label' | 'help'
export type FieldEditorText = { summary: string, label: string, help: string, defaultLabel?: string, helpExample?: string }

interface TranslationSources {
  uiLanguage: () => string
  languages: () => readonly TranslationLanguage[]
  fields: () => readonly RegistryField[]
}

const builtInFields = new Set(['title', 'slug', 'body'])

function builtInKey(field: RegistryField, part: DisplayPart) {
  if (part === 'help' && field.kind === 'relation') return 'admin.content_relation_help'
  if (field.scope !== 'localized' || field.visibility !== 'public' || !builtInFields.has(field.key)) return undefined
  if (part === 'label') return `admin.content_field_${field.key}`
  if (field.key === 'slug' && field.config?.slug) return 'admin.content_field_slug_help'
  return undefined
}

function useTranslationLanguages(options: TranslationSources) {
  const translationLocale = ref(options.uiLanguage())
  const contentLanguages = ref<TranslationLanguage[]>([])
  const { client, request } = usePublicApi()
  let active = true

  const translationLanguages = computed(() => {
    const result = new Map<string, TranslationLanguage>()
    for (const language of [...options.languages(), ...contentLanguages.value]) result.set(language.code, language)
    for (const field of options.fields()) {
      for (const code of Object.keys(field.display_labels ?? {})) {
        if (!result.has(code)) result.set(code, { code, native_name: code })
      }
    }
    return [...result.values()]
  })
  const translationLanguage = computed(() => translationLanguages.value.find(language => language.code === translationLocale.value))

  watch(translationLanguages, (languages) => {
    if (!translationLanguage.value) {
      translationLocale.value = languages.find(language => language.code === options.uiLanguage())?.code ?? languages[0]?.code ?? ''
    }
  }, { immediate: true })

  async function loadContentLanguages() {
    try {
      const result = await request(() => client.listLanguages())
      if (active && result.data) contentLanguages.value = result.data
    }
    catch {
      // Published UI languages and stored translations remain available.
    }
  }

  onBeforeUnmount(() => { active = false })
  return { translationLocale, translationLanguages, translationLanguage, loadContentLanguages }
}

export function useRegistryFieldTranslations(options: TranslationSources & {
  translate: (key: string) => string
  sessionChanged: () => void
}) {
  const languages = useTranslationLanguages(options)
  const { translationLocale } = languages
  const defaults = ref<{ locale: string, messages: Record<string, string> }>()
  const { readWorkspace } = useAdminUI()

  watch([translationLocale, options.uiLanguage, options.languages], async ([language, uiLanguage, catalogs], _, onCleanup) => {
    if (language === uiLanguage || !catalogs.some(catalog => catalog.code === language)) return
    defaults.value = undefined
    let cancelled = false
    onCleanup(() => { cancelled = true })
    try {
      const catalog = await readWorkspace(language)
      if (!cancelled && catalog.language.code === language) {
        defaults.value = { locale: language, messages: catalog.messages }
      }
    }
    catch (error: unknown) {
      if (!cancelled && error instanceof AdminContentError && error.status === 401) options.sessionChanged()
      // The built-in examples are optional. All translations remain editable.
    }
  }, { immediate: true })

  function fieldLabel(field: RegistryField) {
    const custom = ownValue(field.display_labels, options.uiLanguage())?.label
    if (custom?.trim()) return custom
    const key = builtInKey(field, 'label')
    const localized = key && options.translate(key)
    return localized && localized !== key ? localized : field.key
  }

  function displayValue(field: RegistryField, part: DisplayPart) {
    return ownValue(field.display_labels, translationLocale.value)?.[part] ?? ''
  }

  function displayDefault(field: RegistryField, part: DisplayPart) {
    const key = builtInKey(field, part)
    if (!key) return part === 'label' ? field.key : undefined
    if (translationLocale.value === options.uiLanguage()) return options.translate(key)
    if (defaults.value?.locale === translationLocale.value) return defaults.value.messages[key]
    return undefined
  }

  function fieldEditorText({ field, locked }: { field: RegistryField, locked: boolean }): FieldEditorText {
    const help = displayValue(field, 'help')
    return {
      summary: fieldLabel(field) || options.translate(locked ? 'admin.registry_unnamed_field' : 'admin.registry_new_field'),
      label: displayValue(field, 'label'),
      help,
      defaultLabel: displayDefault(field, 'label'),
      helpExample: help ? undefined : displayDefault(field, 'help'),
    }
  }

  function updateLabel(field: RegistryField, part: DisplayPart, value: string) {
    const locale = translationLocale.value
    const entries = new Map(Object.entries(field.display_labels ?? {}))
    const current = entries.get(locale) ?? {}
    const label = part === 'label' ? value : current.label
    const help = part === 'help' ? value : current.help
    if (label || help) entries.set(locale, { label: label || undefined, help: help || undefined })
    else entries.delete(locale)
    field.display_labels = Object.fromEntries(entries)
  }

  return { ...languages, fieldLabel, fieldEditorText, updateLabel }
}
