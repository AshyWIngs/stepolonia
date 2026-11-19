import assert from 'node:assert/strict'

export async function chooseTheme(page, mode) {
  const picker = page.getByTestId('theme-switcher')
  const trigger = picker.locator('summary')
  await trigger.press('Enter')
  await picker.locator(`button[data-theme="${mode}"]`).click()
  assert.equal(await picker.getAttribute('open'), null)
  assert.equal(await trigger.getAttribute('data-current-theme'), mode)
  assert.ok(await trigger.evaluate(element => element === document.activeElement))
}

export async function headerGeometry(page) {
  const geometry = await page.locator('header .header-preference-trigger').evaluateAll(triggers => {
    const rtl = getComputedStyle(document.documentElement).direction === 'rtl'
    const { clientWidth } = document.documentElement
    return triggers.map((trigger) => {
      const box = trigger.getBoundingClientRect()
      return {
        leading: rtl ? clientWidth - box.right : box.left,
        top: box.top + window.scrollY,
        width: box.width,
        height: box.height,
      }
    })
  })
  assert.equal(geometry.length, 2)
  geometry.forEach(({ width, height }) => {
    assert.equal(width, 44)
    assert.equal(height, 44)
  })
  return geometry
}

export function assertStablePreferences(baseline, current) {
  for (const index of [0, 1]) {
    assert.ok(Math.abs(current[index].leading - baseline[index].leading) <= 1, 'Language moved a header control horizontally')
    assert.ok(Math.abs(current[index].top - baseline[index].top) <= 1, 'Language moved a header control vertically')
  }
}
