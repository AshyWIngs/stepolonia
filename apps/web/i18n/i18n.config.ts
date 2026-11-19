import { compileUiMessage } from '../app/utils/ui-message'

export default defineI18nConfig(() => ({
  legacy: false,
  fallbackLocale: false,
  flatJson: true,
  messages: {},
  messageCompiler: compileUiMessage,
}))
