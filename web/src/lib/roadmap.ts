import type { Project, ProjectDependency, ProjectUpdate } from '@/sync/types'

export type Health = ProjectUpdate['health']

export const HEALTHS: readonly { value: Health; label: string; class: string }[] = [
  { value: 'onTrack', label: 'On track', class: 'text-success' },
  { value: 'atRisk', label: 'At risk', class: 'text-warning' },
  { value: 'offTrack', label: 'Off track', class: 'text-danger' },
]

export function healthInfo(health: Health) {
  return HEALTHS.find((h) => h.value === health)!
}

const DAY = 24 * 60 * 60 * 1000
const parse = (date: string) => Date.parse(`${date}T00:00:00Z`)

/** A date some days after (or before) another, both as YYYY-MM-DD. */
export function addDays(date: string, days: number): string {
  return new Date(parse(date) + days * DAY).toISOString().slice(0, 10)
}

/** Whole days from one date to another. */
export function daysBetween(from: string, to: string): number {
  return Math.round((parse(to) - parse(from)) / DAY)
}

export type DragMode = 'move' | 'start' | 'end'

/**
 * A project's dates after its bar is dragged by some days: moved whole, or
 * with one end pulled. An end can't be pulled past the other.
 */
export function dragDates(startDate: string, targetDate: string, days: number, mode: DragMode): { startDate: string; targetDate: string } {
  if (mode === 'move') return { startDate: addDays(startDate, days), targetDate: addDays(targetDate, days) }
  const length = daysBetween(startDate, targetDate)
  if (mode === 'start') return { startDate: addDays(startDate, Math.min(days, length)), targetDate }
  return { startDate, targetDate: addDays(targetDate, Math.max(days, -length)) }
}

/** Whether `from` depends on `to`, directly or through other projects. */
export function dependsOn(dependencies: readonly ProjectDependency[], from: string, to: string): boolean {
  const seen = new Set<string>()
  const queue = [from]
  while (queue.length > 0) {
    const current = queue.pop()!
    if (current === to) return true
    if (seen.has(current)) continue
    seen.add(current)
    for (const d of dependencies) if (d.projectId === current) queue.push(d.dependsOnId)
  }
  return false
}

const FINISHED: readonly Project['status'][] = ['completed', 'canceled']

/**
 * What a project's dependencies say is wrong with its plan, in words: it is
 * due before something it waits on, or waits on something late or off track.
 */
export function dependencyProblems(
  project: Project,
  dependencies: readonly ProjectDependency[],
  projectOf: (id: string) => Project | undefined,
  healthOf: (id: string) => Health | undefined,
  today: string,
): string[] {
  if (FINISHED.includes(project.status)) return []
  const problems: string[] = []
  for (const dependency of dependencies) {
    if (dependency.projectId !== project.id) continue
    const other = projectOf(dependency.dependsOnId)
    if (!other || other.status === 'completed') continue
    if (other.status === 'canceled') {
      problems.push(`Waits on ${other.name}, which was canceled`)
    } else if (project.targetDate && other.targetDate && other.targetDate > project.targetDate) {
      problems.push(`Due ${daysBetween(project.targetDate, other.targetDate)}d before ${other.name}, which it waits on`)
    } else if (other.targetDate && other.targetDate < today) {
      problems.push(`Waits on ${other.name}, which is ${daysBetween(other.targetDate, today)}d late`)
    } else if (healthOf(other.id) === 'offTrack') {
      problems.push(`Waits on ${other.name}, which is off track`)
    }
  }
  return problems
}
