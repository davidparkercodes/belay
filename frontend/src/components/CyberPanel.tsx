import type { ReactNode } from 'react'

interface CyberPanelProps {
  children: ReactNode
  className?: string
}

export default function CyberPanel({ children, className = '' }: CyberPanelProps) {
  return (
    <div className={`cyber-panel p-4 md:p-5 ${className}`}>
      {children}
    </div>
  )
}

interface CyberPanelHeaderProps {
  icon: ReactNode
  title: string
  color?: string
  right?: ReactNode
}

export function CyberPanelHeader({ icon, title, color = '#00f0ff', right }: CyberPanelHeaderProps) {
  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <span style={{ color }}>{icon}</span>
          <h2
            className="font-mono text-sm font-bold tracking-widest"
            style={{ color, textShadow: `0 0 8px ${color}60` }}
          >
            {title}
          </h2>
        </div>
        {right && (
          <span className="font-mono text-xs" style={{ color: '#ffffff' }}>
            {right}
          </span>
        )}
      </div>
      <div className="cyber-divider mb-4" />
    </>
  )
}
