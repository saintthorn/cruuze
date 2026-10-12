/** A number as fixed-width lowercase hex, the way addresses and offsets are written: 0x006d. */
export function hex(value: number, digits = 4): string {
  return '0x' + Math.max(0, Math.floor(value)).toString(16).padStart(digits, '0')
}
