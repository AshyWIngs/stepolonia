'use strict'

const { test } = require('node:test')
const assert = require('node:assert/strict')
const { scan, latestChecks, REQUIRED } = require('./ci-watch.cjs')

function run(name, conclusion, time = '2026-11-15T08:00:00Z') {
  return { name, status: 'completed', conclusion, started_at: time, output: { title: name }, details_url: 'https://example.test/check' }
}

function fixture({ status = 'success', completed = true, enabled = false, owner = 'AshyWIngs', fork = false } = {}) {
  const pr = {
    number: 15, draft: false, user: { login: owner },
    head: { sha: 'abcdef1234567890', ref: 'ssr',
      repo: { full_name: fork ? 'other/stepolonia' : 'AshyWIngs/stepolonia' } },
  }
  const checks = REQUIRED.map(name => run(name, name === 'Codacy Static Code Analysis' ? status : 'success'))
  if (!completed) checks[0].status = 'in_progress'
  const comments = []
  const calls = []
  const listPulls = () => {}
  const listComments = () => {}
  const github = {
    rest: {
      pulls: { list: listPulls },
      issues: {
        listComments,
        createComment: async data => { comments.push({ body: data.body }); calls.push(data.body) },
      },
      checks: { listForRef: async () => ({ data: { check_runs: checks } }) },
    },
    paginate: async method => method === listPulls ? [pr] : method === listComments ? comments : [],
  }
  const outputs = {}
  const core = { info: () => {}, setOutput: (key, value) => { outputs[key] = value } }
  const context = { repo: { owner: 'AshyWIngs', repo: 'stepolonia' } }
  return { enabled, github, core, context, calls, outputs }
}

test('берём результат самой новой попытки проверки', () => {
  const old = run('verify', 'failure', '2026-11-15T08:00:00Z')
  const current = run('verify', 'success', '2026-11-15T09:00:00Z')
  assert.equal(latestChecks([old, current]).get('verify').conclusion, 'success')
})

test('успешные проверки не создают уведомления и не запускают модель', async () => {
  const s = fixture({ enabled: true })
  await scan(s)
  assert.equal(s.calls.length, 0)
  assert.equal(s.outputs.pr, '')
})

test('ожидаем незавершённую Verify, не начинаем преждевременную автоправку', async () => {
  const s = fixture({ status: 'action_required', completed: false, enabled: true })
  await scan(s)
  assert.equal(s.calls.length, 0)
  assert.equal(s.outputs.pr, '')
})

test('без секретов уведомляем один раз, модель не запускаем', async () => {
  const s = fixture({ status: 'action_required' })
  await scan(s)
  await scan(s)
  assert.equal(s.calls.length, 1)
  assert.match(s.calls[0], /Автомёрдж заблокирован/)
  assert.equal(s.outputs.pr, '')
})

test('одна попытка Codex на PR и никакого повторения на том же SHA', async () => {
  const s = fixture({ status: 'failure', enabled: true })
  await scan(s)
  assert.equal(s.calls.length, 2)
  assert.equal(s.outputs.pr, '15')
  assert.equal(s.outputs.branch, 'ssr')
  await scan(s)
  assert.equal(s.calls.length, 2)
  assert.equal(s.outputs.pr, '')
})

test('PR из fork не является входом для автономной правки', async () => {
  const s = fixture({ status: 'failure', enabled: true, fork: true })
  await scan(s)
  assert.equal(s.calls.length, 0)
  assert.equal(s.outputs.pr, '')
})
