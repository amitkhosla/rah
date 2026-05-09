import type { FlowStep } from '../types'

interface FlatFlowUpdate {
  name: string
  instructions: FlowStep[]
  action: 'upsert'
}

/**
 * Converts a potentially-hierarchical flow (with then_steps/else_steps)
 * into a flat array of flow updates for the gateway.
 * Auto-generates fragment flow names for each inline branch.
 */
export function flattenForDeploy(flowName: string, steps: FlowStep[]): FlatFlowUpdate[] {
  const fragments: FlatFlowUpdate[] = []

  function flattenSteps(steps: FlowStep[], contextName: string): FlowStep[] {
    return steps.map((step, idx) => {
      if (step.action !== 'if') return step

      const thenSteps = step.then_steps ?? []
      const elseSteps = step.else_steps ?? []

      // Already has string refs (old format) — leave unchanged
      if (thenSteps.length === 0 && elseSteps.length === 0) return step

      const thenFragName = `__auto_${contextName}_${idx}_then`
      const elseFragName = `__auto_${contextName}_${idx}_else`

      // Recursively flatten nested branches (handles infinite nesting)
      if (thenSteps.length > 0) {
        const flatThen = flattenSteps(thenSteps, `${contextName}_${idx}_then`)
        fragments.push({ name: thenFragName, instructions: flatThen, action: 'upsert' })
      }
      if (elseSteps.length > 0) {
        const flatElse = flattenSteps(elseSteps, `${contextName}_${idx}_else`)
        fragments.push({ name: elseFragName, instructions: flatElse, action: 'upsert' })
      }

      // Return a flat if step with string then/else references
      const { then_steps: _, else_steps: __, ...rest } = step as Record<string, unknown>
      return {
        ...rest,
        action: 'if',
        ...(thenSteps.length > 0 ? { then: thenFragName } : {}),
        ...(elseSteps.length > 0 ? { else: elseFragName } : {}),
      } as FlowStep
    })
  }

  const flatMain = flattenSteps(steps, flowName)
  return [{ name: flowName, instructions: flatMain, action: 'upsert' }, ...fragments]
}
