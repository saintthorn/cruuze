import { shallowReactive } from 'vue'

import { applyMutation, type Mutation, mutationTarget } from './mutations'
import { MODEL_NAMES, type ModelName, type Models, recordKey, type StoredRecord, type SyncAction } from './types'

type Tables = { [M in ModelName]: Map<string, Models[M]> }
type AnyTable = Map<string, Models[ModelName]>

function emptyTables(reactive: boolean): Tables {
  const tables = {} as Record<ModelName, Map<string, unknown>>
  for (const model of MODEL_NAMES) {
    tables[model] = reactive ? shallowReactive(new Map()) : new Map()
  }
  return tables as Tables
}

/** Where a record lives: its model and its key within that model. */
interface Target {
  model: ModelName
  key: string
}

/**
 * The in-memory copy of a workspace.
 *
 * `base` is the state the server has confirmed. `view` is what the UI reads:
 * the base with the user's unconfirmed mutations replayed on top. Keeping the
 * two apart means a rejected mutation is undone by simply dropping it, and a
 * server change that lands underneath pending edits is picked up correctly.
 */
export class ObjectPool {
  private base = emptyTables(false)
  private pending: Mutation[] = []

  /** Reactive. Records are replaced, never mutated in place. */
  readonly view = emptyTables(true)

  /** Replaces the confirmed state wholesale, e.g. after a bootstrap. */
  load(records: Iterable<StoredRecord>) {
    this.base = emptyTables(false)
    for (const { model, key, value } of records) {
      ;(this.base[model] as AnyTable).set(key, value)
    }
    for (const model of MODEL_NAMES) {
      const view = this.view[model] as AnyTable
      view.clear()
      for (const [key, value] of this.base[model]) view.set(key, value)
    }
    this.refresh(this.pending.map(mutationTarget))
  }

  /**
   * Adds confirmed records that weren't part of the last `load`, e.g. an
   * issue's discussion fetched when the issue is opened.
   */
  merge(records: Iterable<StoredRecord>) {
    const targets: Target[] = []
    for (const { model, key, value } of records) {
      ;(this.base[model] as AnyTable).set(key, value)
      targets.push({ model, key })
    }
    this.refresh(targets)
  }

  /** Applies confirmed changes from the sync log. */
  apply(actions: readonly SyncAction[]) {
    for (const action of actions) {
      const { model } = action
      const key = recordKey(model, action.data)
      const base = this.base[model] as AnyTable
      // Archived issues leave the pool, matching what a bootstrap would return.
      const archived = model === 'Issue' && 'archivedAt' in action.data && action.data.archivedAt
      if (action.action === 'D' || archived) base.delete(key)
      else base.set(key, action.data)
      this.refresh([{ model, key }])
    }
  }

  /** Sets the unconfirmed mutations, oldest first. */
  setPending(pending: readonly Mutation[]) {
    const affected = [...this.pending, ...pending].map(mutationTarget)
    this.pending = [...pending]
    this.refresh(affected)
  }

  /** Recomputes the visible copy of the given records. */
  private refresh(targets: Iterable<Target>) {
    for (const { model, key } of targets) {
      let record = (this.base[model] as AnyTable).get(key)
      for (const m of this.pending) {
        const target = mutationTarget(m)
        if (target.model === model && target.key === key) record = applyMutation(m, record)
      }
      const view = this.view[model] as AnyTable
      if (record) view.set(key, record)
      else view.delete(key)
    }
  }
}
