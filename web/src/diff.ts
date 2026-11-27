// Parses the unified diff of a pull request, as GitHub sends it with the media type application/vnd.github.diff.

export type FileStatus = 'added' | 'deleted' | 'renamed' | 'modified'

export interface DiffLine {
  kind: 'add' | 'delete' | 'context' | 'note'
  text: string
  // null for the side that the line is not on.
  oldNumber: number | null
  newNumber: number | null
}

export interface Hunk {
  header: string
  lines: DiffLine[]
}

export interface DiffFile {
  path: string
  // Differs from path only for a renamed file.
  oldPath: string
  status: FileStatus
  binary: boolean
  additions: number
  deletions: number
  hunks: Hunk[]
}

const hunkHeader = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/

// Git quotes a path with unusual characters, and adds the prefix a/ or b/.
function cleanPath(path: string): string {
  const unquoted = path.startsWith('"') && path.endsWith('"') ? path.slice(1, -1) : path
  return unquoted.replace(/^[ab]\//, '')
}

export function parseDiff(text: string): DiffFile[] {
  const files: DiffFile[] = []
  let file: DiffFile | null = null
  let hunk: Hunk | null = null
  let oldNumber = 0
  let newNumber = 0

  for (const line of text.split('\n')) {
    if (line.startsWith('diff --git ')) {
      // The paths of this line are ambiguous with spaces, so the ---, +++, and rename lines replace them when they come.
      const paths = line.slice('diff --git '.length)
      const split = paths.lastIndexOf(' b/')
      const path = cleanPath(split >= 0 ? paths.slice(split + 1) : paths)
      const oldPath = split >= 0 ? cleanPath(paths.slice(0, split)) : path
      file = { path, oldPath, status: 'modified', binary: false, additions: 0, deletions: 0, hunks: [] }
      files.push(file)
      hunk = null
      continue
    }
    if (!file) continue
    const header = hunkHeader.exec(line)
    if (header) {
      hunk = { header: line, lines: [] }
      file.hunks.push(hunk)
      oldNumber = Number(header[1])
      newNumber = Number(header[2])
      continue
    }
    if (!hunk) {
      if (line.startsWith('new file mode')) file.status = 'added'
      else if (line.startsWith('deleted file mode')) file.status = 'deleted'
      else if (line.startsWith('rename from ')) {
        file.status = 'renamed'
        file.oldPath = cleanPath(line.slice('rename from '.length))
      } else if (line.startsWith('rename to ')) file.path = cleanPath(line.slice('rename to '.length))
      else if (line.startsWith('Binary files ') || line === 'GIT binary patch') file.binary = true
      else if (line.startsWith('--- ') && line !== '--- /dev/null') file.oldPath = cleanPath(line.slice(4))
      else if (line.startsWith('+++ ') && line !== '+++ /dev/null') file.path = cleanPath(line.slice(4))
      continue
    }
    if (line.startsWith('+')) {
      hunk.lines.push({ kind: 'add', text: line.slice(1), oldNumber: null, newNumber: newNumber++ })
      file.additions++
    } else if (line.startsWith('-')) {
      hunk.lines.push({ kind: 'delete', text: line.slice(1), oldNumber: oldNumber++, newNumber: null })
      file.deletions++
    } else if (line.startsWith(' ')) {
      hunk.lines.push({ kind: 'context', text: line.slice(1), oldNumber: oldNumber++, newNumber: newNumber++ })
    } else if (line.startsWith('\\')) {
      hunk.lines.push({ kind: 'note', text: line.slice(2), oldNumber: null, newNumber: null })
    }
  }
  for (const item of files) {
    if (item.status === 'added' || item.status === 'deleted') item.oldPath = item.path
  }
  return files
}

// A model can write a path with ./, /, a/, or b/ before it.
export function samePath(path: string, file: DiffFile): boolean {
  const clean = path.replace(/^\.?\//, '').replace(/^[ab]\//, '')
  return clean === file.path || clean === file.oldPath
}

// The hunk and the index of the line that a finding names. An unchanged line has both sides.
export function findLine(file: DiffFile, side: 'new' | 'old', line: number): { hunk: number; index: number } | null {
  for (const [hunk, { lines }] of file.hunks.entries()) {
    const index = lines.findIndex((item) =>
      side === 'old' ? item.kind !== 'add' && item.oldNumber === line : item.kind !== 'delete' && item.newNumber === line,
    )
    if (index >= 0) return { hunk, index }
  }
  return null
}
