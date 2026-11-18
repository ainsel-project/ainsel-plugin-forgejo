import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { DocsPage } from './components/DocsPage'

// HashRouter: GitHub Pages serves static files only (no SPA fallback), so routes
// live after the `#` (e.g. /ainsel-plugin-forgejo/#/docs/quickstart). Every link
// and navigation call inside DocsPage goes through react-router, which makes the
// hash router a drop-in here.
export function App() {
  return (
    <HashRouter>
      <Routes>
        <Route path="/" element={<Navigate to="/docs" replace />} />
        {/* `/docs` without the splat route, so `useParams()['*']` is undefined
            there and DocsPage can default to the first sidebar entry. */}
        <Route path="/docs" element={<DocsPage />} />
        <Route path="/docs/*" element={<DocsPage />} />
        <Route path="*" element={<Navigate to="/docs" replace />} />
      </Routes>
    </HashRouter>
  )
}
