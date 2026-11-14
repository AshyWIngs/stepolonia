# Stepolonia SDK

Типы генерируются `openapi-typescript 7.13.0` из Huma OpenAPI, Fetch-клиент использует `openapi-fetch 0.17.0`. Генератор совместим с закреплённым TypeScript 5.9.3. Нужны Go из `apps/api/go.mod`, Node.js 26.13.1 и npm 11.21.2.

Из корня проекта `make sdk` обновляет OpenAPI JSON и TypeScript, `make sdk-check` сравнивает оба артефакта с результатом новой генерации и запускает ESLint и typecheck. `make check-db` использует существующую одноразовую PostgreSQL и выполняет запросы этим клиентом к настоящему HTTP API.

```ts
import { createPublicClient } from '@stepolonia/sdk'

const client = createPublicClient({ baseUrl: 'https://portal.example' })
const articles = await client.listArticles({ query: { lang: 'pl', limit: 20 } })
```

Создавайте экземпляр клиента на запрос SSR. Redirect обрабатывается с `redirect: 'manual'`, чтобы серверный caller получил 301, Location и JSON `null`. В браузере Fetch возвращает для ручного перенаправления непрозрачный `opaqueredirect`. Получение Location требует серверного вызова. Сетевая ошибка отклоняет Promise, HTTP problem доступен в `error`, статус и заголовки в `response`.

`openapi.json` и `src/schema.d.ts` являются генерируемыми артефактами. Изменяйте зарегистрированные Go/Huma-операции и запускайте генерацию.
