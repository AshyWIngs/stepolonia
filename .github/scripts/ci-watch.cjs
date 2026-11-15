'use strict'

// Проводим один дешёвый опрос GitHub: модель и тесты здесь не запускаются.
const REQUIRED = [
  'verify',
  'SonarCloud Code Analysis',
  'Codacy Static Code Analysis',
  'GitGuardian Security Checks',
]
const OK = new Set(['success', 'neutral', 'skipped'])

function latestChecks(runs) {
  const byName = new Map()
  for (const run of runs) {
    if (!REQUIRED.includes(run.name)) continue
    const previous = byName.get(run.name)
    if (!previous || new Date(run.started_at || run.created_at) > new Date(previous.started_at || previous.created_at)) {
      byName.set(run.name, run)
    }
  }
  return byName
}

async function scan({ github, core, context, enabled }) {
  const { owner, repo } = context.repo
  const pulls = await github.paginate(github.rest.pulls.list, {
    owner, repo, base: 'main', state: 'open', per_page: 100,
  })
  let selected = null
  for (const pr of pulls) {
    // Внешние и черновые PR не являются доверенным входом для автоправок.
    if (pr.draft || pr.head.repo?.full_name !== context.repo.owner + '/' + repo) continue
    const sha = pr.head.sha
    const { data } = await github.rest.checks.listForRef({ owner, repo, ref: sha, per_page: 100 })
    const checks = latestChecks(data.check_runs || [])
    const completed = REQUIRED.every(name => checks.get(name)?.status === 'completed')
    if (!completed) {
      core.info('PR #' + pr.number + ': обязательные проверки ещё выполняются на ' + sha.slice(0, 7))
      continue
    }
    const failed = REQUIRED.filter(name => !OK.has(checks.get(name).conclusion))
    if (!failed.length) continue

    const comments = await github.paginate(github.rest.issues.listComments, {
      owner, repo, issue_number: pr.number, per_page: 100,
    })
    const alertMark = '<!-- stepolonia-ci-watch:alert:' + sha + ' -->'
    const attempts = comments.filter(c => c.body?.includes('<!-- stepolonia-ci-watch:attempt:'))
    if (!comments.some(c => c.body?.includes(alertMark))) {
      const summary = failed.map(name => {
        const check = checks.get(name)
        return '- ' + name + ': ' + check.conclusion + ' (' + (check.output?.title || 'см. GitHub Check') + ')'
      }).join('\n')
      const details = failed.map(name => checks.get(name).details_url).filter(Boolean).map(url => '- ' + url).join('\n')
      await github.rest.issues.createComment({
        owner, repo, issue_number: pr.number,
        body: alertMark + '\n**Автомёрдж заблокирован на ' + sha.slice(0, 7) + '.**\n' +
          summary + '\n\n' + details + '\n\n' +
          (enabled && pr.user.login === owner
            ? 'Автоисправление Codex будет запущено не более одного раза на PR.'
            : 'Автоисправление не запущено: нужен доверенный PR владельца и два настроенных секрета GitHub Actions.'),
      })
    }
    // Один платный Codex-запуск на PR; новый SHA не создаёт бесконечный цикл.
    if (!selected && enabled && pr.user.login === owner && attempts.length === 0
        && pr.head.ref !== 'main' && /^[A-Za-z0-9._/-]+$/.test(pr.head.ref)
        && !pr.head.ref.includes('..') && !pr.head.ref.includes('@{')) {
      await github.rest.issues.createComment({
        owner, repo, issue_number: pr.number,
        body: '<!-- stepolonia-ci-watch:attempt:' + sha + ' -->\n' +
          'Codex: однократная адресная попытка исправления для ' + sha.slice(0, 7) +
          '. Следующие проверки выполняет штатный PR CI; повторные платные попытки не запускаются автоматически.',
      })
      selected = { number: String(pr.number), sha, branch: pr.head.ref }
    }
  }
  core.setOutput('pr', selected?.number || '')
  core.setOutput('sha', selected?.sha || '')
  core.setOutput('branch', selected?.branch || '')
  core.info(selected ? 'Запрос Codex на PR #' + selected.number : 'Новых автоматических исправлений не требуется')
}

module.exports = { scan, latestChecks, REQUIRED, OK }
