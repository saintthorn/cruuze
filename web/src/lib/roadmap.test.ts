import { describe, expect, it } from 'vitest'

import type { Project, ProjectDependency } from '@/sync/types'

import { addDays, daysBetween, dependencyProblems, dependsOn, dragDates, type Health } from './roadmap'

const dep = (projectId: string, dependsOnId: string) => ({ id: `${projectId}>${dependsOnId}`, projectId, dependsOnId }) as ProjectDependency

describe('dates', () => {
  it('adds days across months and years', () => {
    expect(addDays('2026-12-30', 3)).toBe('2027-01-02')
    expect(addDays('2026-03-01', -1)).toBe('2026-02-28')
    expect(daysBetween('2026-01-01', '2026-03-01')).toBe(59)
  })

  it('moves a bar whole', () => {
    expect(dragDates('2026-01-10', '2026-01-20', 7, 'move')).toEqual({ startDate: '2026-01-17', targetDate: '2026-01-27' })
  })

  it("pulls one end, never past the other", () => {
    expect(dragDates('2026-01-10', '2026-01-20', -5, 'start')).toEqual({ startDate: '2026-01-05', targetDate: '2026-01-20' })
    expect(dragDates('2026-01-10', '2026-01-20', 30, 'start')).toEqual({ startDate: '2026-01-20', targetDate: '2026-01-20' })
    expect(dragDates('2026-01-10', '2026-01-20', -30, 'end')).toEqual({ startDate: '2026-01-10', targetDate: '2026-01-10' })
    expect(dragDates('2026-01-10', '2026-01-20', 4, 'end')).toEqual({ startDate: '2026-01-10', targetDate: '2026-01-24' })
  })
})

describe('dependsOn', () => {
  const chain = [dep('launch', 'app'), dep('app', 'api'), dep('docs', 'api')]

  it('follows chains', () => {
    expect(dependsOn(chain, 'launch', 'api')).toBe(true)
    expect(dependsOn(chain, 'api', 'launch')).toBe(false)
    expect(dependsOn(chain, 'docs', 'app')).toBe(false)
    expect(dependsOn(chain, 'app', 'app')).toBe(true)
  })

  it('survives a loop in the data', () => {
    expect(dependsOn([dep('a', 'b'), dep('b', 'a')], 'a', 'c')).toBe(false)
  })
})

describe('dependencyProblems', () => {
  const projects: Record<string, Project> = {}
  const add = (id: string, fields: Partial<Project>) => (projects[id] = { id, name: id, status: 'started', ...fields } as Project)
  const app = add('app', { targetDate: '2026-06-01' })
  add('api', { targetDate: '2026-07-01' })
  add('design', { targetDate: '2026-03-01' })
  add('infra', { targetDate: '2026-05-01' })
  add('legal', { status: 'canceled' })
  add('research', { status: 'completed', targetDate: '2026-09-01' })
  const health: Record<string, Health> = { infra: 'offTrack' }
  const problems = (deps: ProjectDependency[], project = app) =>
    dependencyProblems(project, deps, (id) => projects[id], (id) => health[id], '2026-04-01')

  it('finds nothing wrong with a plan in order', () => {
    expect(problems([])).toEqual([])
    expect(problems([dep('app', 'research'), dep('api', 'app')])).toEqual([])
  })

  it('says when a project is due before what it waits on', () => {
    expect(problems([dep('app', 'api')])).toEqual(['Due 30d before api, which it waits on'])
  })

  it('says when what it waits on is late, off track or canceled', () => {
    expect(problems([dep('app', 'design'), dep('app', 'infra'), dep('app', 'legal')])).toEqual([
      'Waits on design, which is 31d late',
      'Waits on infra, which is off track',
      'Waits on legal, which was canceled',
    ])
  })

  it('has nothing to say about a finished project', () => {
    expect(problems([dep('app', 'api')], { ...app, status: 'completed' })).toEqual([])
  })
})
