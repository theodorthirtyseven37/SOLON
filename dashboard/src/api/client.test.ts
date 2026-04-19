import { describe, it, expect } from 'vitest'
import { extractErrorMessage, errorFromResponse } from './client'

describe('extractErrorMessage', () => {
  const fb = 'FALLBACK'

  it('returns the string when body.error is a string', () => {
    expect(extractErrorMessage({ error: 'bad input' }, fb)).toBe('bad input')
  })

  it('returns body.error.message when present', () => {
    expect(extractErrorMessage({ error: { message: 'rate limited', code: 429 } }, fb)).toBe('rate limited')
  })

  it('JSON-stringifies body.error when it is an object without .message', () => {
    expect(extractErrorMessage({ error: { code: 'E_NO_MODEL', details: 'x' } }, fb))
      .toBe('{"code":"E_NO_MODEL","details":"x"}')
  })

  it('falls back when body.error.message is not a string', () => {
    // Object with .message that is itself an object — this is exactly the
    // shape that produced the original "[object Object]" bug.
    const result = extractErrorMessage({ error: { message: { nested: true } } }, fb)
    expect(result).not.toBe('[object Object]')
    expect(result).toBe('{"message":{"nested":true}}')
  })

  it('falls back to top-level message when there is no .error', () => {
    expect(extractErrorMessage({ message: 'something broke' }, fb)).toBe('something broke')
  })

  it('returns raw string bodies verbatim', () => {
    expect(extractErrorMessage('plain text error', fb)).toBe('plain text error')
  })

  it('returns fallback for null / undefined / empty string', () => {
    expect(extractErrorMessage(null, fb)).toBe(fb)
    expect(extractErrorMessage(undefined, fb)).toBe(fb)
    expect(extractErrorMessage('', fb)).toBe(fb)
  })

  it('returns fallback for bodies with no recognized shape', () => {
    expect(extractErrorMessage({ foo: 'bar' }, fb)).toBe(fb)
  })

  it('handles circular references without throwing', () => {
    const circular: Record<string, unknown> = { code: 'X' }
    circular.self = circular
    expect(() => extractErrorMessage({ error: circular }, fb)).not.toThrow()
    // Can't stringify → falls through to fallback
    expect(extractErrorMessage({ error: circular }, fb)).toBe(fb)
  })
})

describe('errorFromResponse', () => {
  function jsonResponse(status: number, statusText: string, body: unknown): Response {
    return new Response(JSON.stringify(body), {
      status,
      statusText,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  it('extracts the server message from a standard gateway error', async () => {
    const res = jsonResponse(400, 'Bad Request', { error: { message: 'missing model', type: 'Bad Request' } })
    const err = await errorFromResponse(res)
    expect(err).toBeInstanceOf(Error)
    expect(err.message).toBe('missing model')
  })

  it('falls back to "HTTP <status> <statusText>" when the body is not JSON', async () => {
    const res = new Response('<html>502 Bad Gateway</html>', {
      status: 502,
      statusText: 'Bad Gateway',
      headers: { 'Content-Type': 'text/html' },
    })
    const err = await errorFromResponse(res)
    expect(err.message).toBe('HTTP 502 Bad Gateway')
  })

  it('never produces "[object Object]"', async () => {
    // Regression guard for the bug that prompted this refactor: server
    // returns { error: <object without .message> } and the Error constructor
    // used to coerce it to the literal string "[object Object]".
    const res = jsonResponse(500, 'Internal Server Error', { error: { code: 'E_INTERNAL' } })
    const err = await errorFromResponse(res)
    expect(err.message).not.toBe('[object Object]')
    expect(err.message).toContain('E_INTERNAL')
  })
})
