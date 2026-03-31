import { useEffect } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from '../components/Sidebar'
import { useInstancesStore } from '../store/instances'
import { useModeStore } from '../store/mode'
import { useServerStore } from '../store/server'
import { useUIStore } from '../store/ui'

export default function AppLayout() {
  const load = useInstancesStore(s => s.load)
  const mode = useModeStore(s => s.mode)
  const fetchServer = useServerStore(s => s.fetch)
  const collapsed = useUIStore(s => s.sidebarCollapsed)

  useEffect(() => {
    if (mode !== 'local') {
      load()
    }
  }, [load, mode])

  useEffect(() => {
    if (mode !== 'cloud') {
      fetchServer()
    }
  }, [fetchServer, mode])

  return (
    <div className="min-h-screen bg-[var(--bg)]">
      <Sidebar />
      <div className={`transition-all ${collapsed ? 'lg:pl-14' : 'lg:pl-60'}`}>
        <Outlet />
      </div>
    </div>
  )
}
