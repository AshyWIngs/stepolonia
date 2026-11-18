export default defineI18nConfig(() => ({
  legacy: false,
  fallbackLocale: false,
  flatJson: true,
  messages: {},
  // Expand only named {placeholders}. The catalog treats @, |, and HTML as text.
  messageCompiler: message => {
    if (typeof message !== 'string') throw new Error('Expected a string UI catalog entry')
    return (context: { named: (name: string) => unknown }) => message.replace(/\{([a-z][a-z0-9_]*)\}/gu, (_match, name: string) => String(context.named(name)))
  },
}))
