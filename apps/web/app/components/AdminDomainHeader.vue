<script setup lang="ts">
defineProps<{ title?: string, headingId?: string, saveLabel?: string, disabled?: boolean }>()
</script>

<template>
  <header class="admin-domain-header">
    <h3 v-if="title" :id="headingId">{{ title }}</h3>
    <div v-if="saveLabel || $slots.actions" class="admin-domain-actions">
      <button
        v-if="saveLabel" type="submit" class="admin-domain-save" :disabled="disabled"
        :aria-label="saveLabel" :title="saveLabel"
      >
        <AdminIcon name="save" />
      </button>
      <slot name="actions" />
    </div>
  </header>
</template>

<style>
.admin-domain {
  min-inline-size: 0;
  padding: clamp(.75rem, 2vw, 1.25rem);
  border: 1px solid var(--admin-border);
  border-radius: .65rem;
  background: var(--admin-card);
}

.admin-domain-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: .5rem 1rem;
}

.admin-domain-header h3 {
  flex: 1 1 10rem;
  margin: 0;
  min-inline-size: 0;
  overflow-wrap: anywhere;
}

.admin-domain-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: .5rem;
  margin-inline-start: auto;
}

.admin-domain-header button.admin-domain-save,
.draft-bar button.admin-domain-save {
  display: inline-grid;
  place-items: center;
  box-sizing: border-box;
  inline-size: 44px;
  block-size: 44px;
  min-inline-size: 44px;
  padding: 0;
  border: 1px solid var(--admin-border);
  border-radius: .4rem;
  background: var(--admin-action);
  color: var(--admin-action-text);
}

.admin-domain-header button.admin-domain-save:disabled {
  background: var(--admin-card);
  color: var(--admin-text);
  opacity: .65;
}

.admin-domain-save:focus-visible {
  outline: 3px solid var(--admin-focus);
  outline-offset: 3px;
}
</style>
