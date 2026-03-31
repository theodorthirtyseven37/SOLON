import { useEffect, useState } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { useAuth } from './hooks/useAuth'
import { useTheme } from './hooks/useTheme'
import { useModeStore } from './store/mode'
import { localAPI } from './api/local'
import { cloudAPI } from './api/cloud'
import { InstanceProvider } from './contexts/InstanceContext'
import AuthLayout from './layouts/AuthLayout'
import AppLayout from './layouts/AppLayout'

// Instance pages (local + remote shared)
import Home from './pages/Home'
import Chat from './pages/instance/Chat'
import Models from './pages/instance/Models'
import Keys from './pages/instance/Keys'
import Providers from './pages/instance/Providers'
import Sandboxes from './pages/instance/Sandboxes'
import Activity from './pages/instance/Activity'
import InstanceSettings from './pages/instance/InstanceSettings'
import Setup from './pages/instance/Setup'

// Cloud pages
import Login from './pages/cloud/Login'
import AuthCallback from './pages/cloud/AuthCallback'
import Instances from './pages/cloud/Instances'
import InstanceDetail from './pages/cloud/InstanceDetail'
import Billing from './pages/cloud/Billing'
import Team from './pages/cloud/Team'
import AccountSettings from './pages/cloud/AccountSettings'
import Users from './pages/cloud/Users'
// Onboarding merged into Home page

function RequireLocal({ children }: { children: React.ReactNode }) {
  const mode = useModeStore(s => s.mode)
  if (mode === 'cloud') return <Navigate to="/instances" replace />
  return <>{children}</>
}

function RequireCloudAuth({ children }: { children: React.ReactNode }) {
  const { user } = useAuth()
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

function RequireAdmin({ children }: { children: React.ReactNode }) {
  const { user } = useAuth()
  if (!user) return <Navigate to="/login" replace />
  if (user.role !== 'admin') return <Navigate to="/" replace />
  return <>{children}</>
}

function RequireGuest({ children }: { children: React.ReactNode }) {
  const { user } = useAuth()
  const mode = useModeStore(s => s.mode)
  if (mode === 'local') return <Navigate to="/" replace />
  if (user) return <Navigate to="/" replace />
  return <>{children}</>
}

function RootRedirect() {
  const mode = useModeStore(s => s.mode)
  const { user } = useAuth()
  const [redirect, setRedirect] = useState<string | null>(null)

  useEffect(() => {
    if (mode === 'local' || mode === 'hybrid') {
      setRedirect('/home')
      return
    }
    if (!user) {
      setRedirect('/login')
      return
    }
    setRedirect('/home')
  }, [mode, user])

  if (!redirect) return null
  return <Navigate to={redirect} replace />
}

function LocalInstanceWrapper({ children }: { children: React.ReactNode }) {
  return (
    <InstanceProvider api={localAPI} instanceName="Local Instance">
      {children}
    </InstanceProvider>
  )
}

function LocalRoute({ children }: { children: React.ReactNode }) {
  return (
    <RequireLocal>
      <LocalInstanceWrapper>{children}</LocalInstanceWrapper>
    </RequireLocal>
  )
}

export default function App() {
  useTheme()
  const { loading: authLoading } = useAuth()
  const { loading: modeLoading, init } = useModeStore()

  useEffect(() => {
    init()
  }, [init])

  if (modeLoading || authLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--bg)]">
        <div className="text-center">
          <svg className="mx-auto mb-3" width="36" height="36" viewBox="0 0 28 28" fill="none" style={{filter: 'drop-shadow(0 0 6px rgba(108, 99, 255, 0.4))'}}>
            <circle cx="14" cy="14" r="11" fill="var(--text)" />
          </svg>
          <p className="text-sm text-[var(--text-tertiary)]">Loading...</p>
        </div>
      </div>
    )
  }

  return (
    <Routes>
      {/* OAuth callback (no layout) */}
      <Route path="/auth/callback" element={<AuthCallback />} />

      {/* Auth routes (cloud-only) */}
      <Route element={<RequireGuest><AuthLayout /></RequireGuest>}>
        <Route path="/login" element={<Login />} />
      </Route>

      {/* App routes */}
      <Route element={<AppLayout />}>
        <Route path="/" element={<RootRedirect />} />

        {/* Home — works in both local and cloud mode */}
        <Route path="/home" element={<Home />} />

        {/* Local instance routes */}
        <Route path="/chat" element={<LocalRoute><Chat /></LocalRoute>} />
        <Route path="/models" element={<LocalRoute><Models /></LocalRoute>} />
        <Route path="/keys" element={<LocalRoute><Keys /></LocalRoute>} />
        <Route path="/providers" element={<LocalRoute><Providers /></LocalRoute>} />
        <Route path="/sandboxes" element={<LocalRoute><Sandboxes /></LocalRoute>} />
        <Route path="/activity" element={<LocalRoute><Activity /></LocalRoute>} />
        <Route path="/settings" element={<LocalRoute><InstanceSettings /></LocalRoute>} />
        <Route path="/setup" element={<LocalRoute><Setup onComplete={() => window.location.href = '/home'} /></LocalRoute>} />

        {/* Cloud routes */}
        <Route path="/instances" element={<RequireCloudAuth><Instances /></RequireCloudAuth>} />
        <Route path="/instances/:id" element={<RequireCloudAuth><InstanceDetail /></RequireCloudAuth>}>
          <Route index element={<Home />} />
          <Route path="models" element={<Models />} />
          <Route path="keys" element={<Keys />} />
          <Route path="activity" element={<Activity />} />
          <Route path="settings" element={<InstanceSettings />} />
        </Route>
        <Route path="/billing" element={<RequireCloudAuth><Billing /></RequireCloudAuth>} />
        <Route path="/team" element={<RequireCloudAuth><Team /></RequireCloudAuth>} />
        <Route path="/account" element={<RequireCloudAuth><AccountSettings /></RequireCloudAuth>} />

        {/* Admin routes */}
        <Route path="/admin/users" element={<RequireAdmin><Users /></RequireAdmin>} />
      </Route>

      {/* Catch-all */}
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
