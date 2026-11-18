import { describe, expect, it } from 'vitest'
import { servedDocsPath } from './docsPath'

const docsDir = '/repo/docs'
const unpublished = ['design', 'plans']
const served = (requestPath: string) => servedDocsPath(docsDir, requestPath, unpublished)

describe('servedDocsPath', () => {
  it('serves a doc, including one in a published subdirectory', () => {
    expect(served('overview.md')).toBe('/repo/docs/overview.md')
    expect(served('_sidebar.md')).toBe('/repo/docs/_sidebar.md')
    expect(served('img/diagram.svg')).toBe('/repo/docs/img/diagram.svg')
  })

  it('refuses a path that leaves the docs directory', () => {
    expect(served('../README.md')).toBeNull()
    // The case a string prefix test gets wrong: this resolves to a sibling whose name starts
    // with "docs", so `resolved.startsWith(docsDir)` reads it as inside and serves the file.
    expect(served('../docs-site/package.json')).toBeNull()
    expect(served('nested/../../README.md')).toBeNull()
  })

  it('refuses what the build does not publish', () => {
    expect(served('design/system.md')).toBeNull()
    expect(served('plans/role-tool-enforcement.md')).toBeNull()
    // Refusing the directory, not the word: a doc with "design" in its name is a doc.
    expect(served('design-notes.md')).toBe('/repo/docs/design-notes.md')
  })

  it('refuses the directory itself and an absolute path', () => {
    expect(served('')).toBeNull()
    expect(served('/etc/passwd')).toBeNull()
  })
})
