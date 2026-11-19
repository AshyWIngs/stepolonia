import { computed, ref, watch, type Ref } from 'vue'

export const svgNamespace = 'http://www.w3.org/2000/svg'
// This legacy XML namespace is an identifier and is never fetched.
const shortSvgNamespace = 'http://w3.org'

function parseIcon(source: string) {
  if (source.includes('<!DOCTYPE')) return
  const document = new DOMParser().parseFromString(source, 'image/svg+xml')
  const root = document.documentElement
  const namespace = root.namespaceURI
  if (root.localName !== 'svg' || (namespace !== null && namespace !== svgNamespace && namespace !== shortSvgNamespace)) return
  return root
}

async function decodeIconPreview(source: string) {
  try {
    const root = parseIcon(source)
    if (!root?.hasAttribute('viewBox')) return
    if (root.namespaceURI === null) root.setAttribute('xmlns', svgNamespace)
    const serialized = new XMLSerializer().serializeToString(root)
    const svg = root.namespaceURI === shortSvgNamespace
      ? serialized.replace(`xmlns="${shortSvgNamespace}"`, `xmlns="${svgNamespace}"`)
      : serialized
    const image = new Image()
    image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
    await image.decode()
    return svg
  }
  catch { return undefined }
}

export function hasSVGViewBox(source: string) {
  return parseIcon(source)?.hasAttribute('viewBox') === true
}

export function useSvgImagePreview(source: Readonly<Ref<string>>, saved: Readonly<Ref<string>>) {
  const decoded = ref<{ source: string, svg?: string }>()
  watch([source, saved], ([value, savedValue], _, onCleanup) => {
    decoded.value = undefined
    if (!value.trim() || value === savedValue) return
    let cancelled = false
    const timer = setTimeout(() => {
      void decodeIconPreview(value).then((svg) => {
        if (!cancelled) decoded.value = { source: value, svg }
      })
    }, 200)
    onCleanup(() => {
      cancelled = true
      clearTimeout(timer)
    })
  })
  const svg = computed(() => {
    if (source.value === saved.value) return saved.value
    return decoded.value?.source === source.value ? decoded.value.svg ?? '' : ''
  })
  const unavailable = computed(() => decoded.value?.source === source.value && !decoded.value.svg)
  return { svg, unavailable }
}
