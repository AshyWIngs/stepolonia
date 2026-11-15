export default defineI18nConfig(() => ({
  legacy: false,
  fallbackLocale: false,
  flatJson: true,
  messages: {},
  // Контракт БД допускает только именованные {placeholders}; @, | и HTML остаются обычным текстом.
  messageCompiler: message => {
    if (typeof message !== 'string') throw new Error('Ожидался строковый UI-каталог')
    return (context: { named: (name: string) => unknown }) => message.replace(/\{([a-z][a-z0-9_]*)\}/gu, (_match, name: string) => String(context.named(name)))
  },
}))
