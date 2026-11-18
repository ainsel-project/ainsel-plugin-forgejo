import fs from 'node:fs'
import path from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import { servedDocsPath } from './dev-server/docsPath'

// GitHub Pages serves a project site from a subpath named after the repository:
// https://ainsel-project.github.io/ainsel-plugin-forgejo/. The Vite base path
// must match that prefix. Override via env var when hosting elsewhere.
const basePath = process.env.DOCS_SITE_BASE_PATH ?? '/ainsel-plugin-forgejo/'

// The docs site serves the same repository `docs/` directory that the markdown
// lives in (docsify-style: `_sidebar.md` drives the navigation, each page is
// fetched on demand). This plugin makes that directory available to the Vite dev
// server and copies it into the build output so GitHub Pages can serve it as
// static assets — keeping a single source of truth for the docs.
//
// Two directories in `docs/` are not documentation *of the plugin* and are not
// published: `design/` holds the design the wizard implements, `plans/` the
// implementation plans behind it. They stay in git, where the links to them from
// the README and the code work, and stay out of the public artifact.
const UNPUBLISHED = ['design', 'plans']

function repoDocsPlugin(base: string): Plugin {
  const docsDir = path.resolve(process.cwd(), '../docs')
  const escaped = base.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const prefix = new RegExp(`^${escaped}docs/(.+)$`)

  return {
    name: 'ainsel-plugin-forgejo-docs-site-repo-docs',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const m = (req.url || '').match(prefix)
        if (!m) return next()
        const filePath = servedDocsPath(
          docsDir,
          decodeURIComponent(m[1].split('?')[0]),
          UNPUBLISHED,
        )
        if (!filePath) return next()
        fs.readFile(filePath, (err, data) => {
          if (err) return next()
          res.setHeader('Content-Type', 'text/markdown; charset=utf-8')
          res.end(data)
        })
      })
    },
    closeBundle() {
      const distDocs = path.resolve(process.cwd(), 'dist/docs')
      if (!fs.existsSync(docsDir)) return
      // Rebuilt from scratch: a doc removed from git must not survive in the artifact.
      fs.rmSync(distDocs, { recursive: true, force: true })
      for (const entry of fs.readdirSync(docsDir, { withFileTypes: true })) {
        if (UNPUBLISHED.includes(entry.name)) continue
        fs.cpSync(path.join(docsDir, entry.name), path.join(distDocs, entry.name), {
          recursive: true,
        })
      }
    },
  }
}

export default defineConfig({
  base: basePath,
  plugins: [react(), repoDocsPlugin(basePath)],
  server: {
    // 5173 is the port a plain `vite` would pick; the docs site takes the next
    // one so a UI bundle can be served beside it while writing a doc.
    port: 5174,
  },
})
