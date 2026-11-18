import '@testing-library/jest-dom/vitest'

// Node ≥ 22 ships an experimental `localStorage` global that is `undefined`
// unless `--localstorage-file` is passed. Provide a simple in-memory polyfill
// when the real one is missing — theme.ts reads it at import time.
if (typeof globalThis.localStorage === 'undefined' || globalThis.localStorage === null) {
  const store = new Map<string, string>()
  const storage: Storage = {
    get length() {
      return store.size
    },
    clear: () => store.clear(),
    getItem: (k: string) => store.get(k) ?? null,
    key: (i: number) => [...store.keys()][i] ?? null,
    removeItem: (k: string) => {
      store.delete(k)
    },
    setItem: (k: string, v: string) => {
      store.set(k, String(v))
    },
  }
  Object.defineProperty(globalThis, 'localStorage', {
    value: storage,
    writable: true,
    configurable: true,
  })
  Object.defineProperty(window, 'localStorage', {
    value: storage,
    writable: true,
    configurable: true,
  })
}

// jsdom does not implement scrolling: these exist but log "Not implemented" for every
// call, which would bury real output in noise. The docs page scrolls on navigation.
HTMLElement.prototype.scrollTo = () => {}
HTMLElement.prototype.scrollIntoView = () => {}
window.scrollTo = () => {}
