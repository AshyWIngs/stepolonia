# Stepolonia SDK

The SDK generates TypeScript types from the Go/Huma OpenAPI contract with `openapi-typescript 7.13.0`. Its Fetch client uses `openapi-fetch 0.17.0` and TypeScript 5.9.3. Build prerequisites are the Go version in `apps/api/go.mod`, Node.js 26.13.1, and npm 11.21.2.

From the repository root, run `make sdk` to regenerate OpenAPI and TypeScript. `make sdk-check` checks both generated artifacts, ESLint, and TypeScript. `make check-db` uses a disposable PostgreSQL installation to send SDK requests to the live API.

Make resolves the Go executable before npm adds dependency executables to `PATH`. The generator uses `PATH=/usr/bin:/bin`, `GOTOOLCHAIN=local`, and `CGO_ENABLED=0` for the Go process.

```ts
import { createPublicClient } from '@stepolonia/sdk'

const client = createPublicClient({ baseUrl: 'https://portal.example' })
const site = await client.getSite({ query: { lang: 'pl' } })
const articles = await client.listContent({ path: { type_key: 'article' }, query: { language: 'pl', limit: 20 } })
```

`getSite()` returns the published settings and a complete UI catalog. An optional `lang` selects a published catalog. A missing catalog returns the configured fallback and `requested_language`. Send `query.version` from the previous response to require the same publication revision. A revision change returns 409 in `error`. A missing publication returns 503.

Create one client per SSR request. The client uses `redirect: 'manual'`, so a server caller can read HTTP 301, `Location`, and JSON `null`. Browsers expose manual redirects as an opaque `opaqueredirect` response. Reading `Location` requires a server-side call. A network failure rejects the Promise. HTTP problems appear in `error`, and status and headers in `response`.

Edit the registered Go/Huma operations and regenerate `openapi.json` and `src/schema.d.ts` with `make sdk`.
