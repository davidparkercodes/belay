import type { ReactNode } from 'react'

interface CyberStatCardProps {
  icon: ReactNode
  label: string
  value: string
  unit: string
  color: string
}

export default function CyberStatCard({ icon, label, value, unit, color }: CyberStatCardProps) {
  return (
    <div className="cyber-panel p-3 md:p-4">
      <div className="flex items-center gap-2 mb-2">
        <span style={{ color }}>{icon}</span>
        <span
          className="font-mono text-xs tracking-widest font-bold"
          style={{ color, textShadow: `0 0 4px ${color}40` }}
        >
          {label}
        </span>
      </div>
      <div
        className="font-mono text-2xl md:text-3xl font-black tabular-nums"
        style={{ color: '#ffffff', textShadow: `0 0 12px ${color}60, 0 0 30px ${color}20` }}
      >
        {value}
      </div>
      <div
        className="font-mono text-xs tracking-widest mt-0.5"
        style={{ color: '#ffffff' }}
      >
        {unit}
      </div>
    </div>
  )
}
