import { describe, it, expect, beforeEach } from 'vitest'
import { useAuthStore } from '../store/auth'
import { getToken, clearToken } from '../api/client'

describe('Auth store', () => {
  beforeEach(() => {
    localStorage.clear()
    useAuthStore.setState({ user: null, loading: true })
  })

  it('initial state has no user and loading true', () => {
    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.loading).toBe(true)
  })

  it('logout clears token and user', () => {
    useAuthStore.setState({ user: { id: '1', email: 'test@example.com' } as never })
    useAuthStore.getState().logout()

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(getToken()).toBeNull()
  })

  it('loadUser sets loading to false when no token', async () => {
    clearToken()
    await useAuthStore.getState().loadUser()

    const state = useAuthStore.getState()
    expect(state.loading).toBe(false)
    expect(state.user).toBeNull()
  })
})
