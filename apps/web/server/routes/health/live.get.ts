export default defineEventHandler((event) => {
  setHeader(event, 'Cache-Control', 'no-store')
  return { status: 'ok', service: 'sp-web' }
})
