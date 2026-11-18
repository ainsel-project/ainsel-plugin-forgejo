// Shared lazy Mermaid loader, as in AInsel's console: diagrams are rare enough
// that the bundle is only fetched when one is actually rendered.
let mermaidPromise: Promise<(typeof import('mermaid'))['default']> | null = null

export function loadMermaid(): Promise<(typeof import('mermaid'))['default']> {
  if (!mermaidPromise) {
    mermaidPromise = import('mermaid').then((mod) => {
      const mermaid = mod.default
      mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'strict' })
      return mermaid
    })
  }
  return mermaidPromise
}
