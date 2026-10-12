import { describe, expect, it } from 'vitest'

import { parseImportLink } from './importLink'

describe('parseImportLink', () => {
  it('reads a GitHub repository however it is written', () => {
    const want = { source: 'github', repo: 'acme/widgets' }
    for (const link of [
      'acme/widgets',
      ' https://github.com/acme/widgets ',
      'github.com/acme/widgets/issues/12',
      'https://github.com/acme/widgets.git',
      'git@github.com:acme/widgets.git',
      'https://www.github.com/acme/widgets/issues?q=is%3Aopen',
    ]) {
      expect(parseImportLink(link), link).toEqual(want)
    }
    expect(parseImportLink('https://github.com/acme')).toBeNull()
  })

  it('reads a Jira site from any of its pages', () => {
    expect(parseImportLink('https://acme.atlassian.net/browse/ENG-12')).toEqual({ source: 'jira', baseUrl: 'https://acme.atlassian.net' })
    expect(parseImportLink('acme.atlassian.net')).toEqual({ source: 'jira', baseUrl: 'https://acme.atlassian.net' })
    expect(parseImportLink('https://jira.acme.example/secure/Dashboard.jspa')).toEqual({ source: 'jira', baseUrl: 'https://jira.acme.example' })
    expect(parseImportLink('https://tools.acme.example/jira/browse/OPS-3')).toEqual({ source: 'jira', baseUrl: 'https://tools.acme.example/jira' })
    expect(parseImportLink('https://issues.acme.example/projects/OPS/issues')).toEqual({ source: 'jira', baseUrl: 'https://issues.acme.example' })
  })

  it('reads Linear', () => {
    expect(parseImportLink('https://linear.app/acme/team/ENG/active')).toEqual({ source: 'linear' })
  })

  it('gives up on what it does not know', () => {
    for (const link of ['', 'hello', 'https://example.com/', 'ftp://github.com/acme/widgets', 'mailto:a@b.c']) {
      expect(parseImportLink(link), link).toBeNull()
    }
  })
})
