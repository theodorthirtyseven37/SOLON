import { create } from 'zustand'

type Theme = 'light' | 'dark'

interface Toast {
  id: string
  message: string
  type: 'success' | 'error' | 'info'
}

interface UIState {
  theme: Theme
  sidebarOpen: boolean
  sidebarCollapsed: boolean
  toasts: Toast[]
  setTheme: (theme: Theme) => void
  toggleTheme: () => void
  setSidebarOpen: (open: boolean) => void
  toggleSidebarCollapsed: () => void
  addToast: (message: string, type?: Toast['type']) => void
  removeToast: (id: string) => void
}

export const useUIStore = create<UIState>((set, get) => ({
  theme: 'light',
  sidebarOpen: false,
  sidebarCollapsed: localStorage.getItem('solon-sidebar-collapsed') === '1',
  toasts: [],

  setTheme: (theme) => {
    localStorage.setItem('solon-theme', theme)
    document.documentElement.setAttribute('data-theme', theme)
    document.documentElement.classList.toggle('dark', theme === 'dark')
    set({ theme })
  },

  toggleTheme: () => {
    const next = get().theme === 'light' ? 'dark' : 'light'
    get().setTheme(next)
  },

  setSidebarOpen: (open) => set({ sidebarOpen: open }),

  toggleSidebarCollapsed: () => {
    const next = !get().sidebarCollapsed
    localStorage.setItem('solon-sidebar-collapsed', next ? '1' : '0')
    set({ sidebarCollapsed: next })
  },

  addToast: (message, type = 'info') => {
    const id = Date.now().toString()
    set({ toasts: [...get().toasts, { id, message, type }] })
    setTimeout(() => get().removeToast(id), 4000)
  },

  removeToast: (id) => {
    set({ toasts: get().toasts.filter(t => t.id !== id) })
  },
}))
