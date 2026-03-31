import { NavLink, useNavigate } from 'react-router-dom'
import { useUIStore } from '../store/ui'
import { useAuthStore } from '../store/auth'
import { useModeStore } from '../store/mode'
import { useInstancesStore } from '../store/instances'
import { useServerStore } from '../store/server'
import ThemeToggle from './ThemeToggle'

interface NavSection {
  label: string
  items: { to: string; label: string; icon: React.ReactNode }[]
}

const buildSection: NavSection = {
  label: 'Build',
  items: [
    {
      to: '/home',
      label: 'Dashboard',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="7" height="7" /><rect x="14" y="3" width="7" height="7" /><rect x="14" y="14" width="7" height="7" /><rect x="3" y="14" width="7" height="7" /></svg>,
    },
    {
      to: '/chat',
      label: 'Chat',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" /></svg>,
    },
    {
      to: '/models',
      label: 'Models',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2" /><polyline points="2 17 12 22 22 17" /><polyline points="2 12 12 17 22 12" /></svg>,
    },
    {
      to: '/providers',
      label: 'Providers',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z" /><polyline points="22 6 12 13 2 6" /></svg>,
    },
  ],
}

const agentsSection: NavSection = {
  label: 'Agents',
  items: [
    {
      to: '/sandboxes',
      label: 'Sandboxes',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="2" width="20" height="20" rx="2.18" ry="2.18" /><line x1="7" y1="2" x2="7" y2="22" /><line x1="17" y1="2" x2="17" y2="22" /><line x1="2" y1="12" x2="22" y2="12" /><line x1="2" y1="7" x2="7" y2="7" /><line x1="2" y1="17" x2="7" y2="17" /><line x1="17" y1="7" x2="22" y2="7" /><line x1="17" y1="17" x2="22" y2="17" /></svg>,
    },
  ],
}

const manageSection: NavSection = {
  label: 'Manage',
  items: [
    {
      to: '/keys',
      label: 'API Keys',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" /></svg>,
    },
    {
      to: '/activity',
      label: 'Activity',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12" /></svg>,
    },
    {
      to: '/settings',
      label: 'Settings',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg>,
    },
  ],
}

const accountSection: NavSection = {
  label: 'Account',
  items: [
    {
      to: '/billing',
      label: 'Billing',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><rect x="1" y="4" width="22" height="16" rx="2" ry="2" /><line x1="1" y1="10" x2="23" y2="10" /></svg>,
    },
    {
      to: '/team',
      label: 'Team',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" /><circle cx="9" cy="7" r="4" /><path d="M23 21v-2a4 4 0 0 0-3-3.87" /><path d="M16 3.13a4 4 0 0 1 0 7.75" /></svg>,
    },
    {
      to: '/account',
      label: 'Settings',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg>,
    },
  ],
}

function NavItem({ to, label, icon, collapsed, onClick }: { to: string; label: string; icon: React.ReactNode; collapsed: boolean; onClick?: () => void }) {
  return (
    <NavLink
      to={to}
      end={to === '/home'}
      onClick={onClick}
      title={collapsed ? label : undefined}
      className={({ isActive }) =>
        `flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors ${
          isActive
            ? 'bg-[var(--bg-hover)] text-[var(--text)] font-medium'
            : 'text-[var(--text-secondary)] hover:text-[var(--text)] hover:bg-[var(--bg-hover)]'
        } ${collapsed ? 'justify-center' : ''}`
      }
    >
      {icon}
      {!collapsed && label}
    </NavLink>
  )
}

function SectionGroup({ section, collapsed, onItemClick }: { section: NavSection; collapsed: boolean; onItemClick?: () => void }) {
  return (
    <div>
      {!collapsed && (
        <p className="px-3 pt-4 pb-1 text-[10px] font-medium text-[var(--text-tertiary)] uppercase tracking-wider">
          {section.label}
        </p>
      )}
      {collapsed && <div className="pt-3" />}
      {section.items.map(item => (
        <NavItem key={item.to} {...item} collapsed={collapsed} onClick={onItemClick} />
      ))}
    </div>
  )
}

export default function Sidebar() {
  const { sidebarOpen, setSidebarOpen, sidebarCollapsed, toggleSidebarCollapsed } = useUIStore()
  const mode = useModeStore(s => s.mode)
  const user = useAuthStore(s => s.user)
  const instances = useInstancesStore(s => s.instances)
  const version = useServerStore(s => s.version)
  const tunnel = useServerStore(s => s.tunnel)
  const navigate = useNavigate()

  const showLocal = mode === 'local' || mode === 'hybrid'
  const showCloud = mode === 'cloud' || mode === 'hybrid'
  const closeSidebar = () => setSidebarOpen(false)
  const collapsed = sidebarCollapsed

  const sidebarWidth = collapsed ? 'w-14' : 'w-60'

  return (
    <>
      {/* Mobile overlay */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-40 bg-black/50 lg:hidden" onClick={closeSidebar} />
      )}

      <aside className={`fixed top-0 left-0 z-50 h-full ${sidebarWidth} bg-[var(--bg-card)] border-r border-[var(--border)] flex flex-col transition-all lg:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        {/* Logo + collapse toggle */}
        <div className={`flex items-center ${collapsed ? 'justify-center px-2' : 'justify-between px-5'} py-4`}>
          {collapsed ? (
            <button onClick={toggleSidebarCollapsed} className="p-1 rounded-lg hover:bg-[var(--bg-hover)] transition-colors">
              <svg width="20" height="20" viewBox="0 0 28 28" fill="none" style={{filter: 'drop-shadow(0 0 6px rgba(108, 99, 255, 0.4))'}}>
                <circle cx="14" cy="14" r="11" fill="var(--text)" />
              </svg>
            </button>
          ) : (
            <>
              <div className="flex items-center gap-2.5">
                <svg width="24" height="24" viewBox="0 0 28 28" fill="none" style={{filter: 'drop-shadow(0 0 6px rgba(108, 99, 255, 0.4))'}}>
                  <circle cx="14" cy="14" r="11" fill="var(--text)" />
                </svg>
                <span className="font-semibold text-sm text-[var(--text)]">
                  {mode === 'cloud' ? 'Solon Cloud' : 'Solon'}
                </span>
              </div>
              <button onClick={toggleSidebarCollapsed} className="p-1 rounded-lg text-[var(--text-tertiary)] hover:text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] transition-colors">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="11 17 6 12 11 7" /><polyline points="18 17 13 12 18 7" />
                </svg>
              </button>
            </>
          )}
        </div>

        <div className="flex-1 overflow-y-auto px-2 space-y-1">
          {/* Local sections */}
          {showLocal && (
            <>
              <SectionGroup section={buildSection} collapsed={collapsed} onItemClick={closeSidebar} />
              <SectionGroup section={agentsSection} collapsed={collapsed} onItemClick={closeSidebar} />
              <SectionGroup section={manageSection} collapsed={collapsed} onItemClick={closeSidebar} />
            </>
          )}

          {/* Cloud: instances */}
          {showCloud && user && (
            <>
              {!collapsed && (
                <div>
                  <p className="px-3 pt-4 pb-1 text-[10px] font-medium text-[var(--text-tertiary)] uppercase tracking-wider flex items-center justify-between">
                    <span>Instances</span>
                    <button
                      onClick={() => { closeSidebar(); navigate('/instances') }}
                      className="text-[10px] text-[var(--text-tertiary)] hover:text-[var(--text-secondary)] transition-colors"
                    >
                      Manage
                    </button>
                  </p>
                  {instances.length === 0 ? (
                    <button
                      onClick={() => { closeSidebar(); navigate('/instances') }}
                      className="flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-[var(--text-tertiary)] hover:text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] transition-colors w-full"
                    >
                      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                        <circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="16" /><line x1="8" y1="12" x2="16" y2="12" />
                      </svg>
                      Add instance
                    </button>
                  ) : (
                    instances.map(inst => (
                      <NavLink
                        key={inst.id}
                        to={`/instances/${inst.id}`}
                        onClick={closeSidebar}
                        className={({ isActive }) =>
                          `flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors ${
                            isActive
                              ? 'bg-[var(--bg-hover)] text-[var(--text)] font-medium'
                              : 'text-[var(--text-secondary)] hover:text-[var(--text)] hover:bg-[var(--bg-hover)]'
                          }`
                        }
                      >
                        <span className={`w-2 h-2 rounded-full ${
                          inst.status === 'online' ? 'bg-green-400' : inst.status === 'offline' ? 'bg-red-400' : 'bg-gray-400'
                        }`} />
                        {inst.name}
                      </NavLink>
                    ))
                  )}
                </div>
              )}

              {/* Admin */}
              {user.role === 'admin' && !collapsed && (
                <div>
                  <p className="px-3 pt-4 pb-1 text-[10px] font-medium text-[var(--text-tertiary)] uppercase tracking-wider">Admin</p>
                  <NavItem
                    to="/admin/users"
                    label="Users"
                    collapsed={collapsed}
                    icon={<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" /><circle cx="9" cy="7" r="4" /><path d="M23 21v-2a4 4 0 0 0-3-3.87" /><path d="M16 3.13a4 4 0 0 1 0 7.75" /></svg>}
                    onClick={closeSidebar}
                  />
                </div>
              )}

              <SectionGroup section={accountSection} collapsed={collapsed} onItemClick={closeSidebar} />
            </>
          )}
        </div>

        {/* Bottom bar */}
        <div className={`px-2 py-3 border-t border-[var(--border)] ${collapsed ? 'flex flex-col items-center gap-2' : 'flex items-center justify-between'}`}>
          <ThemeToggle />
          {!collapsed && showLocal && !user && mode === 'local' && (
            <button
              onClick={() => { closeSidebar(); navigate('/login') }}
              className="text-xs text-[var(--text-tertiary)] hover:text-[var(--text-secondary)] transition-colors"
            >
              Sign in &rarr;
            </button>
          )}
          {!collapsed && showLocal && (
            <span className="text-[11px] text-[var(--text-tertiary)] flex items-center gap-1.5">
              {tunnel?.enabled && <span className="w-2 h-2 rounded-full bg-green-400" />}
              {version ? `v${version}` : ''}
            </span>
          )}
          {!collapsed && user && (
            <div className="h-6 w-6 rounded-full bg-brand-light flex items-center justify-center text-white text-[10px] font-medium">
              {user.name?.charAt(0).toUpperCase() || '?'}
            </div>
          )}
        </div>
      </aside>
    </>
  )
}
