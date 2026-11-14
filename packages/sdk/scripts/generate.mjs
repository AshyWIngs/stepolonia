import { execFileSync } from 'node:child_process'
import { readFile, writeFile } from 'node:fs/promises'
import openapiTS, { astToString, COMMENT_HEADER } from 'openapi-typescript'

const packageRoot = new URL('../', import.meta.url)
const apiRoot = new URL('../../../apps/api/', import.meta.url)
const schema = execFileSync('go', ['run', './cmd/openapi'], {
  cwd: apiRoot,
  encoding: 'utf8',
  stdio: ['ignore', 'pipe', 'inherit'],
})
const types = COMMENT_HEADER + astToString(await openapiTS(schema))
const artifacts = [
  [new URL('openapi.json', packageRoot), schema],
  [new URL('src/schema.d.ts', packageRoot), types],
]

for (const [path, contents] of artifacts) {
  if (process.argv.includes('--check')) {
    if (await readFile(path, 'utf8') !== contents) {
      throw new Error(`Устарел ${path.pathname}. Выполните npm run generate в packages/sdk.`)
    }
  } else {
    await writeFile(path, contents)
  }
}
