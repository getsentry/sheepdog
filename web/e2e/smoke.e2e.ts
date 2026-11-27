/// <reference types="node" />
import { expect, test, type Route } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { Classification, PullRequest, Requests, Review, Row, RunningTask } from '../src/types'

const screenshots = process.env.SHEEPDOG_SCREENSHOTS || join(tmpdir(), 'sheepdog-screenshots')
const sample = 'getsentry/reviewer-react-native-sample'
const bare = 'getsentry/sentry-capacitor'
const template = [
  '# Rules for Sheepdog. Sheepdog works only with repositories that have this file on the default branch.',
  'version: 1',
  '',
  'classification:',
  '  model: "anthropic/claude-sonnet-5.5"',
  '  ci: false',
  '',
  'lanes:',
  '  safe:',
  '    authors: ["dependabot[bot]"]',
  '    max_changed_lines: 50',
  '    max_score: 2',
  '    no_major_bumps: true',
  '    require_checks: true',
  '    action: approve-and-automerge',
  '',
  'never_automatic:',
  '  paths: [".github/**"]',
  '',
].join('\n')
const setupPR = { number: 1290, url: `https://github.com/${bare}/pull/1290` }
const now = Date.now()
const ago = (minutes: number) => new Date(now - minutes * 60000).toISOString()

const rows: Row[] = [
  {
    number: 2,
    title: 'Bump expo from 57.0.1 to 57.2.0',
    url: `https://github.com/${sample}/pull/2`,
    isDraft: false,
    updatedAt: ago(42),
    additions: 6,
    deletions: 6,
    author: 'dependabot[bot]',
    reasons: ['you'],
    headSha: 'sha2',
    lane: {
      name: 'safe',
      action: 'approve-and-automerge',
      notes: ['Author dependabot[bot]', 'Only package.json and package-lock.json', '12 changed lines', 'Score 1 of 2'],
    },
    classification: { source: 'ci', score: 1, reason: 'A minor version bump of expo.', model: 'anthropic/claude-sonnet-5.5', headSha: 'sha2' },
  },
  {
    number: 3,
    title: 'Document the release steps in the README',
    url: `https://github.com/${sample}/pull/3`,
    isDraft: false,
    updatedAt: ago(180),
    additions: 140,
    deletions: 12,
    author: 'alwx',
    reasons: ['team'],
    headSha: 'sha3',
    lane: { name: 'quick_look', action: 'suggest', notes: ['Needs at most 100 changed lines, has 152'] },
    classification: { source: 'ci', score: 4, reason: 'Documentation only, but long.', model: 'anthropic/claude-sonnet-5.5', headSha: 'sha3' },
  },
  {
    number: 1,
    title: 'Send user feedback from the profile screen to Sentry',
    url: `https://github.com/${sample}/pull/1`,
    isDraft: true,
    updatedAt: ago(60 * 26),
    additions: 88,
    deletions: 4,
    author: 'octocat',
    reasons: ['you'],
    headSha: 'sha1',
    lane: { name: 'manual', action: '', notes: ['Changes src/sentry.ts'] },
  },
]

const stored: Classification = {
  repo: sample,
  number: 1,
  headSha: 'old',
  model: 'anthropic/claude-sonnet-5.5',
  score: 6,
  reason: 'Changes the Sentry setup of the app.',
  diffTruncated: false,
  prUpdatedAt: ago(60 * 30),
  createdAt: ago(60 * 29),
}

const review: Review = {
  repo: sample,
  number: 2,
  headSha: 'sha2',
  model: 'anthropic/claude-sonnet-5.5',
  risk: 'green',
  summary: 'The PR moves expo to 57.2.0 in package.json and the lockfile. Nothing else changes.',
  findings: [
    {
      severity: 'low',
      file: 'package-lock.json',
      line: 8411,
      side: 'new',
      title: 'Transitive updates',
      detail: 'Three transitive packages move to new patch versions.',
    },
  ],
  diffTruncated: false,
  prUpdatedAt: ago(42),
  createdAt: ago(30),
}

const expoDiff = [
  'diff --git a/package.json b/package.json',
  'index 3f1c2a4..8b0d9e1 100644',
  '--- a/package.json',
  '+++ b/package.json',
  '@@ -12,7 +12,7 @@',
  '   "dependencies": {',
  '     "@sentry/react-native": "7.4.0",',
  '-    "expo": "57.0.1",',
  '+    "expo": "57.2.0",',
  '     "react": "19.3.1",',
  '     "react-native": "0.83.2"',
  '   },',
  'diff --git a/package-lock.json b/package-lock.json',
  'index 0a1b2c3..4d5e6f7 100644',
  '--- a/package-lock.json',
  '+++ b/package-lock.json',
  '@@ -8410,9 +8410,9 @@',
  '     "node_modules/expo": {',
  '-      "version": "57.0.1",',
  '-      "resolved": "https://registry.npmjs.org/expo/-/expo-57.0.1.tgz",',
  '-      "integrity": "sha512-old",',
  '+      "version": "57.2.0",',
  '+      "resolved": "https://registry.npmjs.org/expo/-/expo-57.2.0.tgz",',
  '+      "integrity": "sha512-new",',
  '       "license": "MIT",',
  '       "dependencies": {',
  '         "@babel/runtime": "^7.20.0",',
  '',
].join('\n')

const tasks: RunningTask[] = [
  { id: 'T1', kind: 'review', key: `${sample}#3`, title: 'Review of #3', subtitle: sample, startedAt: ago(1.3) },
]

function pullRequest(row: Row): PullRequest {
  return {
    title: row.title,
    body: [
      '<!-- Thank you for the PR -->',
      'Bumps [expo](https://github.com/expo/expo) from 57.0.1 to 57.2.0.',
      '',
      '## Release notes',
      '',
      '- Fixes a crash on start in **Android** release builds.',
      '- `expo-updates` checks for updates in the background.',
      '',
      '| Package | From | To |',
      '| --- | --- | --- |',
      '| expo | 57.0.1 | 57.2.0 |',
      '',
      '<img src="x" onerror="alert(1)">',
    ].join('\n'),
    headRefName: 'dependabot/npm_and_yarn/expo-57.2.0',
    baseRefName: 'main',
    headRefOid: row.headSha,
    changedFiles: 2,
    createdAt: ago(60 * 5),
    updatedAt: row.updatedAt,
    reviewDecision: 'REVIEW_REQUIRED',
    labels: ['dependencies', 'javascript'],
    reviewRequests: ['alwx'],
    latestReviews: [],
    checks: { passed: 5, failed: 0, pending: 0, skipped: 1 },
  }
}

test('signs in, opens a PR, cancels approve and merge, opens the settings, sets up a repository, stars one, refreshes, sets global rules, and resizes the sidebar', async ({ page }) => {
  let signedIn = false
  let repo = ''
  let starred: string[] = []
  let globalRules = ''
  // The settings of each repository that the user changed.
  const repoSettings = new Map<string, { autoRefresh: boolean; autoClassify: boolean }>()
  // The event streams after a refresh wait until the test checks the spinner.
  let refreshHeld = Promise.resolve()
  let releaseRefresh = () => {}
  let setupOpened = false
  let setupBody: unknown = null
  const writes: string[] = []
  const reads: string[] = []

  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
  const settings = () => ({
    repo,
    model: 'anthropic/claude-sonnet-5.5',
    hasOpenRouterKey: true,
    starred,
    autoClassifyRepos: [...repoSettings].filter(([, value]) => value.autoClassify).map(([name]) => name).sort(),
    globalRules,
  })
  const requests = (): Requests =>
    repo === sample
      ? { repo, fetchedAt: ago(0.2), rows, lanes: ['safe', 'docs', 'quick_look'], defaultBranch: 'main' }
      : repo === bare
        ? {
            repo,
            fetchedAt: ago(0.1),
            rows: globalRules ? rows : [],
            lanes: globalRules ? ['safe'] : [],
            defaultBranch: 'main',
            setup: { defaultBranch: 'main', pullRequest: setupOpened ? setupPR : null },
            globalRules: globalRules !== '',
          }
        : { repo, fetchedAt: null, rows: [], lanes: [] }

  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const call = `${request.method()} ${path}`
    if (request.method() !== 'GET') writes.push(call)
    else reads.push(call)
    if (call === 'POST /api/session') {
      signedIn = true
      return json(route, 200, { login: 'alwx', sessionToken: 'not-a-real-token' })
    }
    if (!signedIn) return json(route, 401, { error: 'Sign in first' })
    const star = path.match(/^\/api\/repos\/([^/]+\/[^/]+)\/star$/)
    if (star && request.method() === 'PUT') {
      starred = [...new Set([...starred, star[1]])].sort()
      return json(route, 200, settings())
    }
    if (star && request.method() === 'DELETE') {
      starred = starred.filter((item) => item !== star[1])
      return json(route, 200, settings())
    }
    const settingsOf = path.match(/^\/api\/repos\/([^/]+\/[^/]+)\/settings$/)
    if (settingsOf) {
      const current = repoSettings.get(settingsOf[1]) ?? { autoRefresh: true, autoClassify: false }
      if (request.method() === 'PUT') repoSettings.set(settingsOf[1], { ...current, ...request.postDataJSON() })
      return json(route, 200, { repo: settingsOf[1], ...(repoSettings.get(settingsOf[1]) ?? current) })
    }
    const pull = path.match(/^\/api\/repos\/[^/]+\/[^/]+\/pulls\/(\d+)(\/\w+)?$/)
    switch (call) {
      case 'GET /api/me':
        return json(route, 200, { login: 'alwx' })
      case 'GET /api/settings':
        return json(route, 200, settings())
      case 'PUT /api/settings': {
        const update = request.postDataJSON() as { repo?: string; globalRules?: string }
        repo = update.repo?.toLowerCase() ?? repo
        globalRules = update.globalRules ?? globalRules
        return json(route, 200, settings())
      }
      case 'POST /api/review-requests/refresh':
        refreshHeld = new Promise((resolve) => (releaseRefresh = resolve))
        return route.fulfill({ status: 202 })
      case 'GET /api/repos':
        return json(route, 200, ['getsentry/sentry-cocoa', 'getsentry/sentry-react-native', bare, sample])
      case 'GET /api/models':
        return json(route, 200, ['anthropic/claude-sonnet-5.5', 'openai/gpt-6'])
      case 'GET /api/events':
        await refreshHeld
        // The stream ends after these events, so the app connects again after 2 seconds.
        return route.fulfill({
          status: 200,
          contentType: 'text/event-stream',
          body: `event: tasks\ndata: ${JSON.stringify(tasks)}\n\nevent: requests\ndata: ${JSON.stringify(requests())}\n\n`,
        })
      case `GET /api/repos/${sample}/classifications`:
        return json(route, 200, [stored])
      case `GET /api/repos/${bare}/classifications`:
        return globalRules
          ? json(route, 200, [])
          : json(route, 409, { error: `${bare} has no .github/sheepdog.yml on main. Set it up first, or set global rules in Settings` })
      case 'GET /api/global-rules/template':
        return json(route, 200, { content: template })
      case `GET /api/repos/${bare}/setup/template`:
        return json(route, 200, { content: template })
      case `POST /api/repos/${bare}/setup`:
        setupOpened = true
        setupBody = request.postDataJSON()
        return json(route, 201, setupPR)
    }
    const row = pull && rows.find((item) => item.number === Number(pull[1]))
    if (row && request.method() === 'GET') {
      if (!pull[2]) return json(route, 200, pullRequest(row))
      if (pull[2] === '/review' && row.number === 2) return json(route, 200, review)
      if (pull[2] === '/diff') return json(route, 200, { diff: row.number === 2 ? expoDiff : '', truncated: false })
      if (pull[2] === '/classification' && row.number === 1) return json(route, 200, stored)
    }
    return json(route, 404, { error: 'Not found' })
  })

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Sign in to Sheepdog' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Create token on GitHub' })).toHaveAttribute('href', /scopes=repo,read:org/)
  await page.screenshot({ path: join(screenshots, '1-sign-in.png') })

  await page.getByLabel('GitHub token').fill('ghp_example')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.locator('.sidebar-content').getByText('Select a repository')).toBeVisible()
  expect(await page.evaluate(() => localStorage.length + sessionStorage.length + document.cookie.length)).toBe(0)

  await page.getByRole('button', { name: /Select a repository/ }).click()
  const picker = page.getByRole('dialog', { name: 'Repositories' })
  await picker.getByRole('searchbox').fill('reviewer')
  await picker.getByRole('button', { name: sample }).click()
  await expect(picker).toBeHidden()

  const safe = page.getByRole('region', { name: 'safe' })
  await expect(safe.getByRole('heading')).toContainText('Approve and merge (1)')
  await expect(page.getByRole('region', { name: 'manual' }).getByTitle(/changed after this classification/)).toHaveText('6')
  await safe.getByText('Bump expo from 57.0.1 to 57.2.0').click()
  await expect(page).toHaveTitle('Bump expo from 57.0.1 to 57.2.0')
  await expect(page.getByRole('region', { name: 'Review' })).toContainText('Low risk')
  await expect(page.locator('.markdown').getByRole('cell', { name: '57.2.0' })).toBeVisible()
  await expect(page.getByText('<img src="x" onerror="alert(1)">')).toBeVisible()
  await expect(page.getByRole('link', { name: 'expo', exact: true })).toHaveAttribute('rel', 'noopener noreferrer')
  await page.screenshot({ path: join(screenshots, '2-list-and-detail.png') })
  const changes = page.getByRole('region', { name: 'Changes' })
  await expect(changes.getByRole('heading')).toHaveText('Changes2 files +4 −4')
  await expect(changes.getByRole('region', { name: 'package.json' }).getByRole('row')).toHaveCount(8)
  // The finding of the review shows under its line, the new version line of expo in the lockfile.
  const lock = changes.getByRole('region', { name: 'package-lock.json' })
  await expect(lock.getByRole('button', { name: /package-lock\.json/ })).toContainText('1 finding')
  const finding = lock.locator('tr.comment')
  await expect(finding).toContainText('Transitive updates')
  await expect(finding.locator('xpath=preceding-sibling::tr[1]')).toContainText('8411')
  await expect(finding.locator('xpath=preceding-sibling::tr[1]')).toContainText('"version": "57.2.0"')
  await changes.scrollIntoViewIfNeeded()
  await page.screenshot({ path: join(screenshots, '15-diff.png') })
  // The header of the detail panel lines up with the sidebar header and stays on top while the PR scrolls.
  const detailHeader = page.locator('.detail-header')
  expect((await detailHeader.boundingBox())?.height).toBe((await page.locator('.sidebar > header').boundingBox())?.height)
  await page.setViewportSize({ width: 1440, height: 400 })
  await page.getByRole('main').evaluate((main) => main.scrollTo(0, main.scrollHeight))
  expect(await page.getByRole('main').evaluate((main) => main.scrollTop)).toBeGreaterThan(100)
  expect((await detailHeader.boundingBox())?.y).toBe(0)
  await expect(detailHeader.getByRole('button', { name: 'Review again' })).toBeVisible()
  // The page and the scroll areas do not bounce at their ends.
  expect(
    await page.evaluate(() =>
      ['html', 'body', '.detail', '.sidebar-content'].map((selector) => getComputedStyle(document.querySelector(selector)!).overscrollBehavior),
    ),
  ).toEqual(['none', 'none', 'none', 'none'])
  await page.setViewportSize({ width: 1440, height: 900 })

  await page.getByRole('button', { name: 'Approve and merge', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: 'Approve and merge' })
  await expect(confirmation.getByRole('listitem')).toHaveText('#2 Bump expo from 57.0.1 to 57.2.0')
  await page.screenshot({ path: join(screenshots, '3-confirmation.png') })
  await confirmation.getByRole('button', { name: 'Cancel' }).click()
  await expect(confirmation).toBeHidden()

  await page.getByRole('button', { name: 'Settings', exact: true }).click()
  const settingsDialog = page.getByRole('dialog', { name: 'Settings' })
  await expect(settingsDialog.getByLabel('OpenRouter API key')).toHaveAttribute('placeholder', 'Saved on the server')
  await expect(settingsDialog.getByRole('button', { name: /anthropic\/claude-sonnet-5.5/ })).toBeEnabled()
  await page.screenshot({ path: join(screenshots, '4-settings.png') })
  await page.keyboard.press('Escape')
  await expect(settingsDialog).toBeHidden()

  await page.getByRole('button', { name: /reviewer-react-native-sample/ }).click()
  await picker.getByRole('searchbox').fill('capacitor')
  await picker.getByRole('button', { name: bare }).click()
  const warning = page.getByRole('region', { name: 'Setup' })
  await expect(warning).toContainText(`${bare} has no .github/sheepdog.yml on its default branch main.`)
  await expect(page.getByRole('region', { name: 'safe' })).toBeHidden()
  await expect(page.getByRole('main')).toHaveText('')
  await page.screenshot({ path: join(screenshots, '5-setup-warning.png') })

  await warning.getByRole('button', { name: 'Create sheepdog.yml' }).click()
  const setupDialog = page.getByRole('dialog', { name: 'Create sheepdog.yml' })
  await expect(setupDialog.getByLabel('sheepdog.yml')).toHaveValue(template)
  expect(writes).toEqual(['POST /api/session', 'PUT /api/settings', 'PUT /api/settings'])
  await page.screenshot({ path: join(screenshots, '6-setup-dialog.png') })

  await setupDialog.getByRole('button', { name: 'Open pull request' }).click()
  await expect(setupDialog.getByRole('link', { name: 'pull request #1290' })).toHaveAttribute('rel', 'noopener noreferrer')
  // The next stream of the mock sends the result with the setup PR.
  await expect(warning).toContainText('Sheepdog waits for the merge of pull request #1290', { timeout: 10000 })
  await expect(warning.getByRole('button', { name: 'Create sheepdog.yml' })).toBeHidden()
  await setupDialog.getByRole('button', { name: 'Close' }).click()
  await expect(setupDialog).toBeHidden()
  await page.screenshot({ path: join(screenshots, '7-setup-waiting.png') })

  expect(writes).toEqual(['POST /api/session', 'PUT /api/settings', 'PUT /api/settings', `POST /api/repos/${bare}/setup`])
  expect(reads).not.toContain(`GET /api/repos/${bare}/classifications`)
  expect(setupBody).toEqual({ content: template })

  await page.getByRole('button', { name: /sentry-capacitor/ }).click()
  const names = picker.getByRole('listitem')
  await expect(names.first()).toContainText('getsentry/sentry-cocoa')
  await names.filter({ hasText: 'getsentry/sentry-react-native' }).getByRole('button', { name: 'Star' }).click()
  await expect(names.first()).toContainText('getsentry/sentry-react-native')
  await expect(names.first().getByRole('button', { name: 'Remove star' })).toBeVisible()
  await expect(picker).toBeVisible()
  await picker.getByRole('searchbox').fill('getsentry/sentry')
  await expect(names).toHaveText(['getsentry/sentry-react-native', 'getsentry/sentry-cocoa', bare])
  await picker.getByRole('searchbox').fill('')
  await page.screenshot({ path: join(screenshots, '8-starred-picker.png') })
  expect(starred).toEqual(['getsentry/sentry-react-native'])
  expect(writes.slice(4)).toEqual(['PUT /api/repos/getsentry/sentry-react-native/star'])
  expect(repo).toBe(bare)

  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: /sentry-capacitor/ }).click()
  await picker.getByRole('button', { name: sample }).click()
  await expect(page.getByRole('region', { name: 'safe' })).toBeVisible()
  const updates = page.locator('.sidebar > footer').getByRole('region', { name: 'Updates' })
  await expect(updates).toContainText('Updated at')
  await expect(page.locator('.sidebar-content').getByText('Updated at')).toHaveCount(0)
  const refresh = updates.getByRole('button', { name: 'Refresh' })
  await refresh.click()
  await expect(refresh.getByRole('status', { name: 'Refreshing' })).toBeVisible()
  await expect(refresh).toBeDisabled()
  releaseRefresh()
  await expect(refresh.getByRole('status')).toBeHidden({ timeout: 10000 })
  await expect(refresh).toBeEnabled()

  await expect(updates.getByRole('switch')).toHaveCount(0)
  await page.locator('.sidebar').screenshot({ path: join(screenshots, '9-refresh-footer.png') })

  await updates.getByRole('button', { name: 'Repository settings' }).click()
  const repoDialog = page.getByRole('dialog', { name: 'Repository settings' })
  await expect(repoDialog).toContainText(`These settings apply only to ${sample}.`)
  await expect(repoDialog.getByRole('link', { name: 'Edit configuration' })).toHaveAttribute(
    'href',
    `https://github.com/${sample}/edit/main/.github/sheepdog.yml`,
  )
  const refreshSwitch = repoDialog.getByRole('region', { name: 'Refresh' }).getByRole('switch', { name: 'Auto-refresh' })
  const classifySwitch = repoDialog.getByRole('region', { name: 'Classify' }).getByRole('switch', { name: 'Auto-classify' })
  const reviewSwitch = repoDialog.getByRole('region', { name: 'Review' }).getByRole('switch', { name: 'Auto-review' })
  await expect(refreshSwitch).toBeChecked()
  await expect(classifySwitch).not.toBeChecked()
  await expect(reviewSwitch).toBeDisabled()
  await refreshSwitch.click()
  await expect(refreshSwitch).not.toBeChecked()
  await classifySwitch.click()
  await expect(classifySwitch).toBeChecked()
  await expect.poll(() => repoSettings.get(sample)).toEqual({ autoRefresh: false, autoClassify: true })
  await expect(repoDialog.getByRole('alert')).toHaveCount(0)
  await page.screenshot({ path: join(screenshots, '10-repo-settings.png'), animations: 'disabled' })
  await repoDialog.getByRole('button', { name: 'Close' }).click()
  await expect(repoDialog).toBeHidden()

  await updates.getByRole('button', { name: 'Repository settings' }).click()
  await expect(refreshSwitch).not.toBeChecked()
  await expect(classifySwitch).toBeChecked()
  expect(writes.slice(5)).toEqual([
    'PUT /api/settings',
    'POST /api/review-requests/refresh',
    `PUT /api/repos/${sample}/settings`,
    `PUT /api/repos/${sample}/settings`,
  ])

  await repoDialog.getByRole('button', { name: 'Close' }).click()
  await page.getByRole('button', { name: 'Settings', exact: true }).click()
  const rulesSection = settingsDialog.getByRole('region', { name: 'Global rules' })
  await rulesSection.getByRole('button', { name: 'Start from the template' }).click()
  await expect(rulesSection.getByLabel('Global rules')).toHaveValue(template)
  await rulesSection.getByRole('button', { name: 'Save' }).click()
  await expect(rulesSection.getByText('Saved')).toBeVisible()
  expect(globalRules).toBe(template)
  await page.screenshot({ path: join(screenshots, '11-global-rules-settings.png') })
  await page.keyboard.press('Escape')

  await page.getByRole('button', { name: /reviewer-react-native-sample/ }).click()
  await expect(names).toHaveText(['getsentry/sentry-react-native', sample, 'getsentry/sentry-cocoa', bare])
  await expect(names.nth(1).getByRole('img', { name: 'Auto-classify is on' })).toBeVisible()
  await expect(picker.getByRole('img', { name: 'Auto-classify is on' })).toHaveCount(1)
  await page.screenshot({ path: join(screenshots, '14-auto-classify-picker.png') })
  await picker.getByRole('searchbox').fill('capacitor')
  await picker.getByRole('button', { name: bare }).click()
  const notice = page.getByRole('region', { name: 'Setup' })
  await expect(notice).toContainText('Sheepdog uses your global rules for it, not rules of the repository.')
  await expect(notice).toContainText('Sheepdog waits for the merge of pull request #1290')
  await expect(page.getByRole('region', { name: 'safe' })).toBeVisible()
  await page.screenshot({ path: join(screenshots, '12-global-rules-list.png') })

  const handle = page.getByRole('separator', { name: 'Resize the sidebar' })
  const sidebar = page.locator('.sidebar')
  expect((await sidebar.boundingBox())?.width).toBe(440)
  const box = (await handle.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + 300)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2 + 200, box.y + 300, { steps: 5 })
  await page.mouse.up()
  expect((await sidebar.boundingBox())?.width).toBe(640)
  await page.reload()
  await expect(page.getByRole('region', { name: 'safe' })).toBeVisible()
  expect((await sidebar.boundingBox())?.width).toBe(640)
  await page.screenshot({ path: join(screenshots, '13-wide-sidebar.png') })
})
