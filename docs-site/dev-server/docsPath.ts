import path from 'node:path'

// Which file a dev-server request for `<base>docs/<path>` is allowed to read, or null when it
// is not. Only the dev server asks: the build copies a fixed set of files and publishes them.
//
// A string prefix test on the resolved path is not enough. A request for
// `../docs-site/package.json` resolves to a sibling whose name *starts with* the docs
// directory, so `resolved.startsWith(docsDir)` — the obvious guard — calls it inside and
// serves a file that is not a doc. `path.relative` asks the question that matters instead:
// is this below that directory, rather than above it.
//
// `unpublished` is the list the build uses too (`design`, `plans`): what the site does not
// publish should not be readable in development either, or a doc that reads one here works on
// a laptop and 404s once deployed.
export function servedDocsPath(
  docsDir: string,
  requestPath: string,
  unpublished: string[],
): string | null {
  const filePath = path.resolve(docsDir, requestPath)
  const inside = path.relative(docsDir, filePath)
  if (!inside || inside.startsWith('..') || path.isAbsolute(inside)) return null
  if (unpublished.includes(inside.split(path.sep)[0])) return null
  return filePath
}
