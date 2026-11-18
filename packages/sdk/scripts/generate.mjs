import { execFileSync } from 'node:child_process'
import { readFile, writeFile } from 'node:fs/promises'
import { isAbsolute } from 'node:path'
import { fileURLToPath } from 'node:url'
import openapiTS, { astToString, COMMENT_HEADER } from 'openapi-typescript'

const packageRoot = new URL('../', import.meta.url)
const apiRoot = new URL('../../../apps/api/', import.meta.url)
// Make resolves Go before npm adds node_modules/.bin to PATH.
const goExecutable = process.env.STEPOLONIA_GO_EXECUTABLE
if (!goExecutable || !isAbsolute(goExecutable)) {
  throw new Error('An absolute Go toolchain path is required. Run make sdk or make sdk-check.')
}
const schema = execFileSync(goExecutable, ['run', './cmd/openapi'], {
  cwd: apiRoot,
  // Keep Go helper commands outside npm's dependency paths.
  env: { ...process.env, PATH: '/usr/bin:/bin', GOTOOLCHAIN: 'local', CGO_ENABLED: '0' },
  encoding: 'utf8',
  stdio: ['ignore', 'pipe', 'inherit'],
})
const types = COMMENT_HEADER + astToString(await openapiTS(schema))
process.chdir(fileURLToPath(packageRoot))
if (process.argv.includes('--check')) {
  const [currentSchema, currentTypes] = await Promise.all([
    readFile('openapi.json', 'utf8'),
    readFile('src/schema.d.ts', 'utf8'),
  ])
  if (currentSchema !== schema || currentTypes !== types) {
    throw new Error('Generated SDK artifacts are stale. Run make sdk.')
  }
} else {
  await Promise.all([
    writeFile('openapi.json', schema),
    writeFile('src/schema.d.ts', types),
  ])
}
