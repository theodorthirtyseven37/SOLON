import { describe, it, expect, beforeEach } from 'vitest'
import { getToken, setToken, clearToken } from '../api/client'

describe('Token management', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('returns null when no token is stored', () => {
    expect(getToken()).toBeNull()
  })

  it('stores and retrieves a token', () => {
    setToken('test-token-123')
    expect(getToken()).toBe('test-token-123')
  })

  it('clears the token', () => {
    setToken('test-token-123')
    clearToken()
    expect(getToken()).toBeNull()
  })

  it('overwrites existing token', () => {
    setToken('first-token')
    setToken('second-token')
    expect(getToken()).toBe('second-token')
  })
})
