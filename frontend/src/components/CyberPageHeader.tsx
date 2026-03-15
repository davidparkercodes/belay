import type { ReactNode } from 'react'

interface CyberPageHeaderProps {
  icon: ReactNode
  title: string
  subtitle?: string
  color?: string
  right?: ReactNode
}

export default function CyberPageHeader({ icon, title, subtitle, color = '#00f0ff', right }: CyberPageHeaderProps) {
  return (
    <div className="flex flex-col md:flex-row md:items-end md:justify-between mb-6 gap-3">
      <div>
        <div className="flex items-center gap-3 mb-1">
          <span style={{ color }}>{icon}</span>
          <h1
            className="text-2xl md:text-3xl font-black font-mono tracking-tight"
            style={{
              color: '#ffffff',
              textShadow: `0 0 10px ${color}50, 0 0 40px ${color}20`,
            }}
          >
            {title}
          </h1>
        </div>
        {subtitle && (
          <div
            className="font-mono text-xs tracking-widest ml-0.5"
            style={{ color: '#ffffff' }}
          >
            {subtitle}
          </div>
        )}
      </div>
      {right}
    </div>
  )
}
