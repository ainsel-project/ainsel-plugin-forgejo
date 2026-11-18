// Shared lazy Mermaid loader: diagrams are rare enough that the bundle is only
// fetched when one is actually rendered.
//
// The initialisation follows AInsel's console (frontend/src/primitives/mermaid.ts) with one
// difference: the theme. The console is dark, so a dark diagram is always right there; this
// site honours the stored theme like the rest of it does, and a dark diagram on a light page
// is unreadable. Mermaid takes its theme at initialisation and the loader caches the module,
// so a diagram keeps the theme the page was opened in.
let mermaidPromise: Promise<(typeof import('mermaid'))['default']> | null = null

export function loadMermaid(): Promise<(typeof import('mermaid'))['default']> {
  if (!mermaidPromise) {
    mermaidPromise = import('mermaid').then((mod) => {
      const mermaid = mod.default
      const dark = document.documentElement.dataset.theme === 'dark'
      mermaid.initialize({
        startOnLoad: false,
        theme: dark ? 'dark' : 'default',
        securityLevel: 'strict',
      })
      return mermaid
    })
  }
  return mermaidPromise
}
