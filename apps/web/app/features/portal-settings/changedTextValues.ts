export function changedTextValues(saved: Record<string, string> | undefined, edited: Record<string, string>) {
  return Object.fromEntries(Object.entries(edited).filter(([key, value]) => saved?.[key] !== value))
}
