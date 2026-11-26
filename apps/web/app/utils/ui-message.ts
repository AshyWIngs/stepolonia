export function compileUiMessage(message: unknown) {
  if (typeof message !== 'string') throw new Error('Expected a string UI catalog entry')
  // The catalog treats punctuation and HTML as text. Only named placeholders expand.
  return (context: { named: (name: string) => unknown }) =>
    message.replace(/\{([a-z][a-z0-9_]*)\}/gu, (_match, name: string) => String(context.named(name)))
}
