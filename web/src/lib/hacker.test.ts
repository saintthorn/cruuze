import { describe, expect, it } from 'vitest'

import { hex } from './hacker'

describe('hex', () => {
  it('pads to a fixed width', () => {
    expect(hex(255)).toBe('0x00ff')
    expect(hex(0)).toBe('0x0000')
    expect(hex(70_000, 2)).toBe('0x11170')
    expect(hex(-3)).toBe('0x0000')
  })
})
