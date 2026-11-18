import { describe, expect, it, vi, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { App } from './App'

// DocsPage fetches docs/_sidebar.md and docs/<slug>.md at runtime. Stub the
// network with a minimal two-file docs tree — the shape the real docs/ has.
const sidebarMd = [
  '## Getting Started',
  '',
  '- [Quick Start](quickstart)',
  '- [Setup Wizard](setup)',
  '',
  '## Reference',
  '',
  '- [HTTP API](api-reference)',
  '',
].join('\n')

const quickstartMd = [
  '# Quick Start',
  '',
  'Run the [setup wizard](setup.md) before anything else.',
  '',
  'It finishes by [creating the trio](setup.md#creating-the-trio), and never by',
  '[reaching the unreachable](setup.md#the-unreachable).',
  '',
  '## What you need',
  '',
  'A Forgejo site-admin token.',
  '',
].join('\n')

const setupMd = [
  '# Setup wizard',
  '',
  '## Creating the trio',
  '',
  'The wizard creates the trio.',
  '',
].join('\n')

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input)
  if (url.endsWith('_sidebar.md')) return new Response(sidebarMd, { status: 200 })
  if (url.endsWith('quickstart.md')) return new Response(quickstartMd, { status: 200 })
  if (url.endsWith('setup.md')) return new Response(setupMd, { status: 200 })
  return new Response('not found', { status: 404 })
})

/** Stub the docs tree and mount the site — at a route if the test needs one;
 *  HashRouter reads the URL, so that is how a deep link arrives. */
function renderDocs(at?: string) {
  vi.stubGlobal('fetch', fetchMock)
  if (at) window.location.hash = at
  render(<App />)
}

describe('App', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    cleanup()
    // HashRouter keeps its route in the URL, and jsdom's URL outlives the render.
    // Left alone, the next test would mount on the route the last one navigated to.
    window.location.hash = ''
  })

  it('renders the sidebar sections and defaults to the first doc', async () => {
    renderDocs()

    // Section headings and every entry from _sidebar.md.
    expect(await screen.findByText('Getting Started')).toBeInTheDocument()
    expect(screen.getByText('Reference')).toBeInTheDocument()
    expect(screen.getAllByText('Setup Wizard').length).toBeGreaterThan(0)
    expect(screen.getByRole('link', { name: 'HTTP API' })).toBeInTheDocument()

    // Content of the first doc, rendered from markdown.
    expect(await screen.findByText('A Forgejo site-admin token.')).toBeInTheDocument()
  })

  it('routes a relative doc link to the doc rather than the repository file', async () => {
    renderDocs()
    // The link sits inside a sentence, so its paragraph cannot be matched by its
    // whole text — scope the query to the article column instead of the page,
    // where the sidebar's own "Setup Wizard" entry would also answer.
    const paragraph = await screen.findByText('A Forgejo site-admin token.')
    const article = paragraph.closest('.docs-content-col') as HTMLElement
    const link = within(article).getByRole('link', { name: 'setup wizard' })
    // The href is a real URL under the site's base path — not a root-relative
    // '/docs/setup', which 404s on GitHub Pages when the tab opens it itself —
    // and never a 'setup.md' path, which has no route either.
    expect(link.getAttribute('href')).toMatch(/docs\/setup$/)
    expect(link.getAttribute('href')).not.toMatch(/\.md$/)

    // And clicking it navigates in place rather than loading a page.
    fireEvent.click(link)
    expect(await screen.findByText('The wizard creates the trio.')).toBeInTheDocument()
  })

  it('lands on the heading a deep link names', async () => {
    // Another doc's section: the route changes, the markdown arrives a moment later,
    // and only then does the heading exist — with the id the scroll looks for.
    const scrollIntoView = vi.spyOn(HTMLElement.prototype, 'scrollIntoView')
    renderDocs('/docs/quickstart')
    fireEvent.click(await screen.findByRole('link', { name: 'creating the trio' }))
    expect(await screen.findByRole('heading', { name: 'Creating the trio' })).toBeInTheDocument()
    expect(scrollIntoView).toHaveBeenCalledWith(expect.objectContaining({ block: 'start' }))
    scrollIntoView.mockRestore()
  })

  it('leaves the page alone when the linked heading is not there', async () => {
    const scrollIntoView = vi.spyOn(HTMLElement.prototype, 'scrollIntoView')
    renderDocs('/docs/quickstart')
    fireEvent.click(await screen.findByRole('link', { name: 'reaching the unreachable' }))
    expect(await screen.findByRole('heading', { name: 'Creating the trio' })).toBeInTheDocument()
    expect(scrollIntoView).not.toHaveBeenCalled()
    scrollIntoView.mockRestore()
  })

  it('offers the next doc in the sidebar order below the article', async () => {
    renderDocs()
    await screen.findByText('A Forgejo site-admin token.')
    const next = document.querySelector('.docs-next') as HTMLAnchorElement
    expect(next).not.toBeNull()
    expect(next.getAttribute('href')).toMatch(/#\/docs\/setup$/)
    // The first doc has nothing before it.
    expect(document.querySelector('.docs-prev')).toBeNull()
  })
})
