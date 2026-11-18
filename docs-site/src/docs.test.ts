import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'

// The docs are the site's content, so they are checked here rather than by a
// reader: the sidebar must name files that exist, every file must be reachable
// from the sidebar, and no doc may link to a slug that has no page. All three
// fail silently in the published site — an empty page, an orphan, a dead click —
// and all three are a one-line fix when a test says which line.

const docsDir = path.resolve(process.cwd(), '../docs')

// Only the top level: `docs/design/` and `docs/plans/` are not published (see
// vite.config.ts), so the sidebar does not name them and their links are not the
// site's to keep honest.
const files = fs.readdirSync(docsDir).filter((f) => f.endsWith('.md'))

const sidebar = fs.readFileSync(path.join(docsDir, '_sidebar.md'), 'utf8')

/** Link targets in a markdown file, with code stripped first: a `[example](text)`
 *  inside a fence or an inline span is an example of markdown, not a link — and
 *  the docs are full of instructions that write markdown out as text. */
function linksIn(source: string): string[] {
  const withoutCode = source.replace(/^```[\s\S]*?^```$/gm, '').replace(/`[^`\n]*`/g, '')
  const out: string[] = []
  for (const m of withoutCode.matchAll(/(?<!!)\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
    out.push(m[1])
  }
  return out
}

/** A target that points at another doc rather than the outside world. */
function isInternalDocLink(target: string): boolean {
  return !/^(https?:|mailto:|#|data:)/.test(target) && !/^\.\.?\//.test(target)
}

/** The file a doc link names, with any anchor and a `.md` suffix taken off. */
function targetFile(target: string): string {
  return `${target.split('#')[0].replace(/\.md$/, '')}.md`
}

describe('docs/', () => {
  it('has a markdown file for every entry in the sidebar', () => {
    const missing = linksIn(sidebar)
      .map(targetFile)
      .filter((file) => !fs.existsSync(path.join(docsDir, file)))
    expect(missing).toEqual([])
  })

  it('reaches every published doc from the sidebar', () => {
    const linked = new Set(linksIn(sidebar).map(targetFile))
    // _sidebar.md is the navigation itself, not a page it points at.
    const orphans = files
      .filter((f) => f !== '_sidebar.md')
      .filter((f) => !linked.has(f))
      .map((f) => f.replace(/\.md$/, ''))
    expect(orphans).toEqual([])
  })

  it('links only to docs that exist', () => {
    const broken: string[] = []
    for (const file of files) {
      if (file === '_sidebar.md') continue
      const source = fs.readFileSync(path.join(docsDir, file), 'utf8')
      for (const target of linksIn(source)) {
        if (!isInternalDocLink(target)) continue
        if (!fs.existsSync(path.join(docsDir, targetFile(target)))) {
          broken.push(`${file} -> ${target}`)
        }
      }
    }
    expect(broken).toEqual([])
  })

  it('opens every doc with a title heading', () => {
    const untitled = files
      .filter((f) => f !== '_sidebar.md')
      .filter((f) => !/^#\s+\S/m.test(fs.readFileSync(path.join(docsDir, f), 'utf8')))
    expect(untitled).toEqual([])
  })
})
