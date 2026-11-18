import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

// The published docs render with AInsel's own design system, so a doc reads the
// same in the site as it does on the console's Docs page. The styles are
// vendored under src/design (see the headers there) because this repository
// builds without AInsel's frontend.
import './design/tokens.css'
import './design/fonts.css'
import './design/base.css'
import './design/primitives.css'
import './design/site.css'
import { initTheme } from './theme'
import { App } from './App'

initTheme()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
