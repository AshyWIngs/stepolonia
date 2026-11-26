<script setup lang="ts">
import { onUpdated } from 'vue'
import { portalSettingsPages } from '~/utils/admin-site-settings'
import { accessPages, domains, hasSubpages, isPortalTool, portalPages } from './AdminWorkspaceNavigation'
import type { AdminTool, AdminWorkspaceNavigation } from './AdminWorkspaceNavigation'

const props = defineProps<{
  navigation: AdminWorkspaceNavigation
  locked: boolean
  translate: (key: string) => string
  collectionLabel: (entry: { type_key: string, display_name?: string }) => string
}>()
const showLabels = defineModel<boolean>('showLabels', { required: true })
const {
  activeTool, expandedTool, portalPage, contentCollections, contentOffset, contentSelection, collectionPageError,
  collectionNavigationLoading, registryCreating, registrySelection, enabledCollections, accessSection,
} = props.navigation

let compactFocus: { tool: AdminTool, opener: HTMLButtonElement } | undefined

function navigationName(tool: AdminTool) {
  return props.translate(tool === 'registry' ? 'admin.workspace_structure' : `admin.${tool}`)
}

function selectCompactTool(tool: AdminTool, event: MouseEvent) {
  if (event.detail === 0 && event.currentTarget instanceof HTMLButtonElement &&
      hasSubpages(tool) && expandedTool.value !== tool && !props.locked) {
    compactFocus = { tool, opener: event.currentTarget }
  }
  props.navigation.selectTool(tool)
}

function clearCompactFocus() {
  compactFocus = undefined
}

onUpdated(() => {
  if (!compactFocus) return
  const { tool, opener } = compactFocus
  if (document.activeElement !== opener || expandedTool.value !== tool) {
    compactFocus = undefined
    return
  }
  const first = document.getElementById(`admin-nav-${tool}`)?.querySelector<HTMLButtonElement>('button:not(:disabled)')
  if (!first?.getClientRects().length) return
  compactFocus = undefined
  first.focus()
})

function closeNavigationGroup(tool: AdminTool, event: KeyboardEvent) {
  if (expandedTool.value !== tool) return
  event.stopPropagation()
  expandedTool.value = undefined
  const compact = document.getElementById(`admin-rail-button-${tool}`)
  const labelled = document.getElementById(`admin-nav-button-${tool}`)
  if (compact?.getClientRects().length) compact.focus()
  else labelled?.focus()
}
</script>

<template>
  <nav class="admin-navigation" :class="{ 'admin-nav-show-labels': showLabels }" :aria-label="translate('admin.name')">
    <div class="admin-nav-mobile-tools">
      <button type="button" :aria-pressed="showLabels" @click="showLabels = !showLabels">
        <AdminIcon name="menu" />
        <span>{{ translate('admin.navigation_labels') }}</span>
      </button>
    </div>
    <ul class="admin-nav-rail">
      <li v-for="tool in domains" :key="tool">
        <button
          :id="`admin-rail-button-${tool}`" type="button" class="admin-nav-rail-button"
          :class="{ 'admin-nav-active': activeTool === tool }" :aria-label="navigationName(tool)" :title="navigationName(tool)"
          :aria-current="activeTool === tool ? 'location' : undefined"
          :aria-expanded="hasSubpages(tool) ? expandedTool === tool : undefined"
          :aria-controls="hasSubpages(tool) ? `admin-nav-${tool}` : undefined"
          :aria-disabled="locked || undefined"
          @click="selectCompactTool(tool, $event)" @blur="clearCompactFocus" @keydown.esc="closeNavigationGroup(tool, $event)"
        >
          <AdminIcon :name="tool" />
        </button>
      </li>
    </ul>
    <ul class="admin-nav-domains">
      <li
        v-for="tool in domains" :key="tool" :class="{ 'admin-nav-open': expandedTool === tool }"
        @keydown.esc="closeNavigationGroup(tool, $event)"
      >
        <button
          :id="`admin-nav-button-${tool}`" type="button" class="admin-nav-parent" :class="{ 'admin-nav-active': activeTool === tool }"
          :aria-current="activeTool === tool && !hasSubpages(tool) ? 'page' : undefined"
          :aria-expanded="hasSubpages(tool) ? expandedTool === tool : undefined"
          :aria-controls="hasSubpages(tool) ? `admin-nav-${tool}` : undefined"
          :aria-disabled="locked || undefined" @click="navigation.selectTool(tool)"
        >
          <AdminIcon :name="tool" />
          <span class="admin-nav-text">{{ navigationName(tool) }}</span>
          <span v-if="hasSubpages(tool)" class="admin-nav-chevron" aria-hidden="true" />
        </button>
        <p v-if="expandedTool === tool" class="admin-nav-mobile-title" aria-hidden="true">{{ navigationName(tool) }}</p>
        <ul v-if="hasSubpages(tool)" v-show="expandedTool === tool" :id="`admin-nav-${tool}`" class="admin-nav-children">
          <template v-if="isPortalTool(tool)">
            <li v-for="page in portalPages.filter(item => portalSettingsPages[item].section === tool)" :key="page">
              <button
                type="button" :aria-current="activeTool === tool && portalPage === page ? 'page' : undefined"
                :aria-disabled="locked || undefined" @click="navigation.selectPortalPage(page)"
              >
                <AdminIcon :name="page" />
                <span class="admin-nav-text">{{ translate(portalSettingsPages[page].label) }}</span>
              </button>
            </li>
          </template>
          <template v-else-if="tool === 'content'">
            <li v-for="entry in contentCollections" :key="entry.type_id">
              <button
                type="button" dir="auto" :aria-current="activeTool === tool && contentSelection === entry.type_key ? 'page' : undefined"
                :aria-disabled="locked || undefined" @click="navigation.selectContent(entry.type_key)"
              >
                <AdminIcon name="collection" :icon-svg="entry.icon_svg" />
                <span class="admin-nav-text" dir="auto">{{ collectionLabel(entry) }}</span>
              </button>
            </li>
            <li v-if="contentOffset !== null">
              <button
                type="button" :disabled="collectionNavigationLoading || locked"
                @click="navigation.loadCollectionNavigation('content', contentOffset)"
              >
                <AdminIcon name="more" />
                <span class="admin-nav-text">{{ translate('admin.content_more_collections') }}</span>
              </button>
            </li>
            <li v-if="collectionPageError" class="admin-error" role="alert">{{ translate(collectionPageError) }}</li>
          </template>
          <template v-else-if="tool === 'registry'">
            <li>
              <button
                type="button" :aria-current="activeTool === tool && !registrySelection && !registryCreating ? 'page' : undefined"
                :aria-disabled="locked || undefined" @click="navigation.selectRegistry()"
              >
                <AdminIcon name="registry" />
                <span class="admin-nav-text">{{ translate('admin.content_collections') }}</span>
              </button>
            </li>
            <li v-for="entry in enabledCollections" :key="entry.type_id">
              <button
                type="button" dir="auto" :aria-current="activeTool === tool && registrySelection === entry.type_key ? 'page' : undefined"
                :aria-disabled="locked || undefined" @click="navigation.selectRegistry(entry.type_key)"
              >
                <AdminIcon name="collection" :icon-svg="entry.icon_svg" />
                <span class="admin-nav-text" dir="auto">{{ collectionLabel(entry) }}</span>
              </button>
            </li>
          </template>
          <template v-else>
            <li v-for="page in accessPages" :key="page">
              <button
                type="button" :aria-current="activeTool === tool && accessSection === page ? 'page' : undefined"
                :aria-disabled="locked || undefined" @click="navigation.selectAccessSection(page)"
              >
                <AdminIcon :name="page" />
                <span class="admin-nav-text">{{ translate(`admin.${page}`) }}</span>
              </button>
            </li>
          </template>
        </ul>
      </li>
    </ul>
  </nav>
</template>

<style scoped>
.admin-navigation {
  min-width: 0;
  padding: .5rem;
  border-radius: .65rem;
  background: var(--admin-card);
}

.admin-nav-domains, .admin-nav-children {
  display: grid;
  gap: .125rem;
  margin: 0;
  padding: 0;
  list-style: none;
}

.admin-nav-domains > li { min-inline-size: 0; }

.admin-nav-children {
  margin-block: .25rem .5rem;
  margin-inline-start: 1rem;
  padding-inline-start: .5rem;
  border-inline-start: 1px solid var(--admin-border);
}

.admin-navigation button {
  display: flex;
  align-items: center;
  gap: .5rem;
  inline-size: 100%;
  text-align: start;
}

.admin-nav-text { flex: 1; min-inline-size: 0; overflow-wrap: anywhere; }

.admin-nav-mobile-tools, .admin-nav-rail, .admin-nav-mobile-title { display: none; }
.admin-nav-children button { font-weight: 500; }
.admin-navigation .admin-nav-active:not([aria-current]) { background: var(--admin-bg); }

.admin-nav-chevron {
  flex: 0 0 .45rem;
  block-size: .45rem;
  margin-inline-end: .2rem;
  border-inline-end: 2px solid currentColor;
  border-block-end: 2px solid currentColor;
  transform: rotate(45deg);
}

[aria-expanded="true"] > .admin-nav-chevron { transform: rotate(225deg); }

.admin-navigation [aria-current] {
  border-color: var(--admin-action);
  background: var(--admin-action);
  color: var(--admin-action-text);
}

.admin-navigation button[aria-disabled="true"] {
  opacity: .65;
  cursor: default;
}

@media (max-width: 48rem) {
  .admin-nav-domains, .admin-nav-children { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .admin-nav-open { grid-column: 1 / -1; }
}

@media (max-width: 40rem) {
  .admin-nav-mobile-tools {
    display: flex;
    justify-content: flex-end;
  }

  .admin-nav-mobile-tools button { inline-size: auto; }
  .admin-navigation.admin-nav-show-labels .admin-nav-domains { grid-template-columns: minmax(0, 1fr); }

  .admin-navigation:not(.admin-nav-show-labels) {
    .admin-nav-domains { grid-template-columns: minmax(0, 1fr); }
    .admin-nav-parent, .admin-nav-domains > li:not(.admin-nav-open) { display: none; }
    .admin-nav-mobile-title {
      display: block;
      margin: .75rem .5rem .25rem;
      font-weight: 650;
      overflow-wrap: anywhere;
    }

    .admin-nav-rail {
      display: grid;
      grid-template-columns: repeat(5, minmax(44px, 1fr));
      gap: .25rem;
      margin: 0;
      padding: 0;
      list-style: none;
    }

    .admin-nav-rail-button { justify-content: center; padding: .5rem; }
  }

}

@media (max-width: 24rem) {
  .admin-nav-domains, .admin-nav-children { grid-template-columns: minmax(0, 1fr); }
}

@media (width > 48rem) {
  .admin-navigation { position: sticky; inset-block-start: 1rem; max-block-size: calc(100dvh - 2rem); overflow: auto; }
}
</style>
