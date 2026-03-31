import { Outlet } from 'react-router-dom'

export default function AuthLayout() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--bg)] px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <svg className="mx-auto mb-3" width="36" height="36" viewBox="0 0 28 28" fill="none" style={{filter: 'drop-shadow(0 0 6px rgba(108, 99, 255, 0.4))'}}>
            <circle cx="14" cy="14" r="11" fill="var(--text)" />
          </svg>
          <h1 className="text-xl font-semibold text-[var(--text)]">Solon Cloud</h1>
        </div>
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-card)] p-6 shadow-[var(--shadow)]">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
