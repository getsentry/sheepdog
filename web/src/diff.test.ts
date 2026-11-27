import { describe, expect, it } from 'vitest'
import { parseDiff } from './diff'

const sample = [
  'diff --git a/src/app.ts b/src/app.ts',
  'index 1111111..2222222 100644',
  '--- a/src/app.ts',
  '+++ b/src/app.ts',
  '@@ -10,4 +10,5 @@ export function start() {',
  '   const a = 1',
  '-  const b = 2',
  '+  const b = 3',
  '+  const c = 4',
  ' ',
  '   return a',
  '@@ -40,2 +41,2 @@',
  '-old',
  '+new',
  '\\ No newline at end of file',
  'diff --git a/docs/new file.md b/docs/new file.md',
  'new file mode 100644',
  'index 0000000..3333333',
  '--- /dev/null',
  '+++ b/docs/new file.md',
  '@@ -0,0 +1,2 @@',
  '+# Title',
  '+Text',
  'diff --git a/gone.txt b/gone.txt',
  'deleted file mode 100644',
  'index 4444444..0000000',
  '--- a/gone.txt',
  '+++ /dev/null',
  '@@ -1 +0,0 @@',
  '-bye',
  'diff --git a/old/name.ts b/new/name.ts',
  'similarity index 90%',
  'rename from old/name.ts',
  'rename to new/name.ts',
  'diff --git a/logo.png b/logo.png',
  'index 5555555..6666666 100644',
  'Binary files a/logo.png and b/logo.png differ',
  '',
].join('\n')

describe('diff', () => {
  it('reads files, statuses, counts, and line numbers', () => {
    const files = parseDiff(sample)
    expect(files.map((file) => [file.path, file.oldPath, file.status, file.binary, file.additions, file.deletions])).toEqual([
      ['src/app.ts', 'src/app.ts', 'modified', false, 3, 2],
      ['docs/new file.md', 'docs/new file.md', 'added', false, 2, 0],
      ['gone.txt', 'gone.txt', 'deleted', false, 0, 1],
      ['new/name.ts', 'old/name.ts', 'renamed', false, 0, 0],
      ['logo.png', 'logo.png', 'modified', true, 0, 0],
    ])
    const [first, second] = files[0].hunks
    expect(first.header).toBe('@@ -10,4 +10,5 @@ export function start() {')
    expect(first.lines.map((line) => [line.kind, line.oldNumber, line.newNumber, line.text])).toEqual([
      ['context', 10, 10, '  const a = 1'],
      ['delete', 11, null, '  const b = 2'],
      ['add', null, 11, '  const b = 3'],
      ['add', null, 12, '  const c = 4'],
      ['context', 12, 13, ''],
      ['context', 13, 14, '  return a'],
    ])
    expect(second.lines.map((line) => [line.kind, line.oldNumber, line.newNumber])).toEqual([
      ['delete', 40, null],
      ['add', null, 41],
      ['note', null, null],
    ])
    expect(second.lines[2].text).toBe('No newline at end of file')
    expect(files[1].hunks[0].lines.map((line) => line.newNumber)).toEqual([1, 2])
  })

  it('reads quoted paths and an empty diff', () => {
    const files = parseDiff('diff --git "a/caf\\303\\251.txt" "b/caf\\303\\251.txt"\n--- "a/x y.txt"\n+++ "b/x y.txt"\n@@ -1 +1 @@\n-a\n+b\n')
    expect(files[0].path).toBe('x y.txt')
    expect(parseDiff('')).toEqual([])
  })
})
