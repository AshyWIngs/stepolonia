import ru from './locales/ru.json'
import pl from './locales/pl.json'
import kk from './locales/kk.json'
import en from './locales/en.json'

export default defineI18nConfig(() => ({
  legacy: false,
  fallbackLocale: false,
  messages: { ru, pl, kk, en },
}))
