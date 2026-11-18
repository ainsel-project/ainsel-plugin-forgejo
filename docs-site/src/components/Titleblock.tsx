// Vendored from AInsel @ 5f8802d8 — frontend/src/layout/Titleblock.tsx
import type { ReactNode } from 'react'
import '../design/titleblock.css'

type TitleblockProps = {
  crumbs?: ReactNode
  title: ReactNode
  actions?: ReactNode
}

export function Titleblock({ crumbs, title, actions }: TitleblockProps) {
  return (
    <header className="titleblock">
      <div>
        {crumbs ? <div className="crumbs">{crumbs}</div> : null}
        <h2>{title}</h2>
      </div>
      {actions ? <div className="actions">{actions}</div> : null}
    </header>
  )
}
