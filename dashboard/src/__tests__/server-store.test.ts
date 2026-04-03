import { describe, it, expect, beforeEach } from 'vitest'
import { useServerStore } from '../store/server'

describe('Server store', () => {
  beforeEach(() => {
    useServerStore.setState({
      version: '',
      status: 'unknown',
      tunnel: null,
      totalMemoryMB: 0,
    })
  })

  it('initial state is unknown', () => {
    const state = useServerStore.getState()
    expect(state.status).toBe('unknown')
    expect(state.version).toBe('')
    expect(state.tunnel).toBeNull()
    expect(state.totalMemoryMB).toBe(0)
  })

  it('fetch sets offline when API unreachable', async () => {
    // fetch will fail in test environment (no server running)
    await useServerStore.getState().fetch()
    const state = useServerStore.getState()
    expect(state.status).toBe('offline')
  })
})
