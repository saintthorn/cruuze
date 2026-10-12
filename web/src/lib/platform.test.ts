import { describe, expect, it } from 'vitest'

import type { Issue } from '@/sync/types'

import { issuesToCsv, toCsv } from './csv'
import { branchName } from './issues'
import { snippetParts } from './search'

describe('snippetParts', () => {
  it('splits highlighted and plain text', () => {
    expect(snippetParts('see the <mark>login</mark> screen and <mark>log</mark>s')).toEqual([
      { text: 'see the ', mark: false },
      { text: 'login', mark: true },
      { text: ' screen and ', mark: false },
      { text: 'log', mark: true },
      { text: 's', mark: false },
    ])
  })

  it('drops Markdown syntax, including a link cut off by the excerpt', () => {
    const text = (snippet: string) => snippetParts(snippet).map((p) => (p.mark ? `{${p.text}}` : p.text)).join('')
    expect(text('Looks like a \\*\\*<mark>race</mark> <mark>condition</mark>\\*\\*. [@Ada Lovelace](mention://user/1) can you check')).toBe(
      'Looks like a {race} {condition}. @Ada Lovelace can you check',
    )
    expect(text('## Steps\n1. Open a `<mark>draft</mark>` and see [the docs](https://exa')).toBe('Steps 1. Open a {draft} and see [the docs')
  })

  it('keeps other markup as literal text', () => {
    expect(snippetParts('<b>bold</b> <img src=x onerror=alert(1)>')).toEqual([
      { text: '<b>bold</b> <img src=x onerror=alert(1)>', mark: false },
    ])
    expect(snippetParts('')).toEqual([])
    expect(snippetParts('<mark>all</mark>')).toEqual([{ text: 'all', mark: true }])
  })
})

describe('toCsv', () => {
  it('quotes only what needs quoting', () => {
    expect(toCsv([['a', 'b c', 'd,e', 'say "hi"', 'two\nlines']])).toBe('a,b c,"d,e","say ""hi""","two\nlines"\r\n')
  })

  it('defuses spreadsheet formulas', () => {
    expect(toCsv([['=SUM(A1)', '+1', '-2', '@x', 'a=b']])).toBe("'=SUM(A1),'+1,'-2,'@x,a=b\r\n")
  })
})

describe('issuesToCsv', () => {
  it('writes the columns the importer reads', () => {
    const issue = {
      id: 'i1',
      stateId: 's1',
      title: 'Fix, then ship',
      description: '',
      priority: 2,
      assigneeId: 'u1',
      labelIds: ['l1', 'gone', 'l2'],
      dueDate: '2026-11-05',
      createdAt: '2026-10-01T10:00:00Z',
    } as unknown as Issue
    const csv = issuesToCsv([issue, { ...issue, priority: 0, assigneeId: undefined, labelIds: [], dueDate: undefined }], {
      identifier: () => 'ENG-1',
      state: () => ({ name: 'In Progress' }) as never,
      user: (id) => (id ? ({ email: 'ada@example.com' } as never) : undefined),
      label: (id) => (id === 'gone' ? undefined : ({ name: id === 'l1' ? 'bug' : 'infra' } as never)),
    })
    expect(csv.split('\r\n')).toEqual([
      'ID,Title,Description,Status,Priority,Assignee,Labels,Due date,Created',
      'ENG-1,"Fix, then ship",,In Progress,High,ada@example.com,"bug, infra",2026-11-05,2026-10-01T10:00:00Z',
      'ENG-1,"Fix, then ship",,In Progress,,,,,2026-10-01T10:00:00Z',
      '',
    ])
  })
})

describe('branchName', () => {
  it('combines the person, the identifier and the title', () => {
    expect(branchName('ENG-12', 'Fix the login redirect!', 'Ada Lovelace')).toBe('ada/eng-12-fix-the-login-redirect')
    expect(branchName('ENG-12', 'Café  déjà vu', 'bob@example.com')).toBe('bob/eng-12-cafe-deja-vu')
    expect(branchName('ENG-12', '???', '')).toBe('eng-12')
  })

  it('stays short', () => {
    const name = branchName('ENG-12', 'word '.repeat(40), 'Ada')
    expect(name.length).toBeLessThanOrEqual(64)
    expect(name.endsWith('-')).toBe(false)
  })
})
