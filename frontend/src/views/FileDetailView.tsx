import { useEffect, useState } from 'react'
import { useLocation, Link } from 'react-router-dom'
import { NavArrowLeft, HardDrive, Code } from 'iconoir-react'
import type { BelayEvent } from '../types'
import * as api from '../services/api'
import EventRow from '../components/EventRow'
import CyberPageHeader from '../components/CyberPageHeader'
import CyberPanel, { CyberPanelHeader } from '../components/CyberPanel'

export default function FileDetailView() {
  const location = useLocation()
  // Path is everything after /files/
  const rawPath = decodeURIComponent(location.pathname.replace('/files/', ''))
  const filePath = (rawPath.includes('..') || rawPath.startsWith('/')) ? '' : rawPath
  const [events, setEvents] = useState<BelayEvent[]>([])
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [showContent, setShowContent] = useState(false)

  useEffect(() => {
    if (!filePath) return
    setLoading(true)
    api.getFileHistory(filePath, 50)
      .then((data) => setEvents(data.events || []))
      .finally(() => setLoading(false))
  }, [filePath])

  const loadContent = async (hash: string) => {
    try {
      const text = await api.getFileContent(hash)
      setContent(text)
      setShowContent(true)
    } catch {
      setContent('Failed to load content')
      setShowContent(true)
    }
  }

  const fileName = filePath.split('/').pop() || filePath
  const latestEvent = events[0]

  return (
    <div className="p-4 md:p-6 max-w-5xl font-mono">
      {/* Back link */}
      <Link
        to="/files"
        className="inline-flex items-center gap-2 px-4 py-2 mb-6 font-mono text-xs tracking-widest transition-all"
        style={{
          color: '#00f0ff',
          border: '1px solid rgba(0, 240, 255, 0.25)',
          borderRadius: '2px',
          background: 'rgba(0, 240, 255, 0.05)',
          textShadow: '0 0 8px rgba(0, 240, 255, 0.4)',
        }}
      >
        <NavArrowLeft className="w-3.5 h-3.5" />
        [ BACK TO FILES ]
      </Link>

      <CyberPageHeader
        icon={<HardDrive className="w-6 h-6" />}
        title={fileName}
        subtitle={filePath}
      />

      {/* View content button */}
      {latestEvent && latestEvent.content_hash && (
        <div className="mb-6">
          <button
            onClick={() => showContent ? setShowContent(false) : loadContent(latestEvent.content_hash)}
            className="inline-flex items-center gap-2 px-4 py-2 font-mono text-xs tracking-widest font-bold transition-all"
            style={{
              color: '#00f0ff',
              border: '1px solid rgba(0, 240, 255, 0.3)',
              borderRadius: '2px',
              background: 'rgba(0, 240, 255, 0.05)',
              textShadow: '0 0 6px rgba(0, 240, 255, 0.4)',
            }}
          >
            <Code className="w-3.5 h-3.5" />
            {showContent ? '[ HIDE CONTENT ]' : '[ VIEW LATEST CONTENT ]'}
          </button>
        </div>
      )}

      {/* Content viewer */}
      {showContent && content !== null && (
        <div className="mb-6">
          <CyberPanel>
            <CyberPanelHeader
              icon={<Code className="w-4 h-4" />}
              title="FILE CONTENT"
              right="LATEST VERSION"
            />
            <pre
              className="font-mono text-xs overflow-x-auto max-h-96 scrollbar-cyber p-1"
              style={{
                color: '#e0e8ff',
                lineHeight: '1.6',
              }}
            >
              {content}
            </pre>
          </CyberPanel>
        </div>
      )}

      {/* History section */}
      {loading ? (
        <div className="flex flex-col items-center justify-center py-20">
          <HardDrive
            className="w-8 h-8 mb-3"
            style={{ color: '#00f0ff', filter: 'drop-shadow(0 0 8px rgba(0, 240, 255, 0.6))' }}
          />
          <p
            className="font-mono text-sm tracking-widest"
            style={{ color: '#00f0ff', textShadow: '0 0 8px rgba(0, 240, 255, 0.4)' }}
          >
            LOADING FILE DATA...
          </p>
        </div>
      ) : (
        <CyberPanel>
          <CyberPanelHeader
            icon={<HardDrive className="w-4 h-4" />}
            title="EVENT HISTORY"
            right={`${events.length} EVENTS`}
          />
          {events.length === 0 ? (
            <div className="py-12 text-center">
              <p
                className="font-mono text-sm tracking-widest"
                style={{ color: '#ffffff' }}
              >
                NO EVENTS FOUND
              </p>
              <p
                className="font-mono text-xs tracking-wider mt-1"
                style={{ color: 'rgba(0, 240, 255, 0.7)' }}
              >
                No history recorded for this file
              </p>
            </div>
          ) : (
            <div className="space-y-0">
              {events.map((event) => (
                <EventRow key={event.event_id} event={event} showFile={false} />
              ))}
            </div>
          )}
        </CyberPanel>
      )}
    </div>
  )
}
