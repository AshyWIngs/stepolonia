import { computed, ref, type Ref } from 'vue'
import { hasSVGViewBox, useSvgImagePreview } from '~/composables/useSvgImagePreview'
import { readManagedCollection, saveCollectionIcon, type ManagedCollection } from '~/utils/admin-content'

export function useRegistryIcon(collection: Readonly<Ref<ManagedCollection | undefined>>) {
  const draft = ref('')
  const invalid = ref(false)
  const conflict = ref(false)
  const failure = ref('')
  const saved = ref(false)
  const currentSvg = computed(() => collection.value?.icon_svg ?? '')
  const changed = computed(() => draft.value !== currentSvg.value)
  const { svg: preview, unavailable: previewUnavailable } = useSvgImagePreview(draft, currentSvg)

  function discard() {
    draft.value = currentSvg.value
    invalid.value = false
  }

  function clearFeedback() {
    failure.value = ''
    saved.value = false
  }

  function reset(svg: string) {
    draft.value = svg
    invalid.value = false
    conflict.value = false
    clearFeedback()
  }

  function clearDraftFeedback() {
    invalid.value = false
    saved.value = false
  }

  function hasValidViewBox() {
    return !draft.value.trim() || hasSVGViewBox(draft.value)
  }

  function acceptFieldResult(result: ManagedCollection, previous?: ManagedCollection) {
    const pending = previous && changed.value && draft.value !== (result.icon_svg ?? '')
    if (!pending) {
      draft.value = result.icon_svg ?? ''
      if (conflict.value) {
        conflict.value = false
        failure.value = ''
      }
      return result
    }
    if (result.icon_version !== previous.icon_version) {
      conflict.value = true
      failure.value = 'admin.registry_conflict'
    }
    return { ...result, icon_version: previous.icon_version }
  }

  async function save(current: ManagedCollection, csrf: string) {
    const result = await saveCollectionIcon(current, draft.value, csrf)
    const version = current.version === result.previous_version ? result.version : current.version
    return { ...current, version, icon_svg: result.icon_svg, icon_version: result.icon_version }
  }

  function acceptSaved(result: ManagedCollection) {
    draft.value = result.icon_svg ?? ''
    saved.value = true
  }

  async function reload(current: ManagedCollection) {
    const fresh = await readManagedCollection(current.type_key)
    return { ...current, icon_svg: fresh.icon_svg, icon_version: fresh.icon_version }
  }

  return {
    draft, invalid, conflict, failure, saved, changed, preview, previewUnavailable,
    discard, clearFeedback, reset, clearDraftFeedback, hasValidViewBox, acceptFieldResult, save, acceptSaved, reload,
  }
}
