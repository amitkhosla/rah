/**
 * dsl_serialize.test.ts  —  Tests for pattern condition serialization
 * Tests round-trip: parse DSL → edit in UI → serialize back → parse again
 */
import { serializePatternCondition, serializeDSL } from './dsl_serialize'
import { parseDSL, parsePatternCondition } from './dsl_parse'
import type { PatternCondition, FlowStep } from '../types'

// Test helper: deep equality check
function objectsEqual(a: any, b: any): boolean {
  if (typeof a !== typeof b) return false
  if (a === null && b === null) return true
  if (a === null || b === null) return false
  if (typeof a !== 'object') return a === b
  const keysA = Object.keys(a).sort()
  const keysB = Object.keys(b).sort()
  if (keysA.length !== keysB.length) return false
  if (!keysA.every((k, i) => k === keysB[i])) return false
  return keysA.every(k => objectsEqual(a[k], b[k]))
}

// Test suite 1: serializePatternCondition returns correct object format
describe('serializePatternCondition', () => {
  test('basic pattern condition with all fields', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'header',
      sourceKey: 'x-service',
      pattern: '^(api|data).*',
      strategy: 'regex',
      flags: 'i',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.type).toBe('pattern_match')
    expect(serialized.source).toBe('header')
    expect(serialized.sourceKey).toBe('x-service')
    expect(serialized.pattern).toBe('^(api|data).*')
    expect(serialized.strategy).toBe('regex')
    expect(serialized.flags).toBe('i')
  })

  test('omits strategy when it is auto', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'query',
      sourceKey: 'filter',
      pattern: 'test',
      strategy: 'auto',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.strategy).toBeUndefined()
  })

  test('omits sourceKey when not set', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'body',
      pattern: '.*error.*',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.sourceKey).toBeUndefined()
  })

  test('omits flags when not set', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'path',
      pattern: '/api/v1/.*',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.flags).toBeUndefined()
  })

  test('handles pattern with special regex characters', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'header',
      sourceKey: 'x-token',
      pattern: '^[A-Za-z0-9+/=]+$',
      flags: 'm',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.pattern).toBe('^[A-Za-z0-9+/=]+$')
  })

  test('handles pattern with escaped quotes', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'body',
      pattern: 'message\\s*=\\s*".*"',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.pattern).toBe('message\\s*=\\s*".*"')
  })

  test('includes multiple flags as single string', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'header',
      sourceKey: 'x-service',
      pattern: '.*SERVICE.*',
      flags: 'im',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.flags).toBe('im')
  })
})

// Test suite 2: round-trip serialization in if steps
describe('serializeDSL with pattern conditions', () => {
  test('round-trip: parse → serialize → parse with pattern condition', () => {
    // Original DSL text with pattern condition
    const dslText = `if ({"type":"pattern_match","source":"header","sourceKey":"x-service","pattern":"^(api|data).*","flags":"i"}) {
  return(200, "matched")
} else {
  return(404, "not_matched")
}`

    // 1. Parse DSL
    const parsed = parseDSL(dslText)
    expect(parsed).toHaveLength(1)
    const ifStep = parsed[0] as any
    expect(ifStep.action).toBe('if')

    // Should have parsed the JSON condition
    const condition = ifStep.condition
    expect(condition).toBeDefined()
    expect(typeof condition).toBe('object')
    if (typeof condition === 'object' && condition.type === 'pattern_match') {
      expect(condition.source).toBe('header')
      expect(condition.sourceKey).toBe('x-service')
      expect(condition.pattern).toBe('^(api|data).*')
      expect(condition.flags).toBe('i')
    }

    // 2. Serialize back to DSL
    const serialized = serializeDSL(parsed)
    expect(serialized).toContain('if (')
    expect(serialized).toContain('"type":"pattern_match"')
    expect(serialized).toContain('"source":"header"')
    expect(serialized).toContain('"sourceKey":"x-service"')
    expect(serialized).toContain('pattern":"^(api|data).*')
    expect(serialized).toContain('"flags":"i"')

    // 3. Parse again
    const reparsed = parseDSL(serialized)
    expect(reparsed).toHaveLength(1)
    const ifStep2 = reparsed[0] as any
    expect(ifStep2.action).toBe('if')

    // Verify condition matches original
    const condition2 = ifStep2.condition
    expect(condition2).toBeDefined()
    if (
      typeof condition === 'object' &&
      condition.type === 'pattern_match' &&
      typeof condition2 === 'object' &&
      condition2.type === 'pattern_match'
    ) {
      expect(condition2.source).toBe(condition.source)
      expect(condition2.sourceKey).toBe(condition.sourceKey)
      expect(condition2.pattern).toBe(condition.pattern)
      expect(condition2.flags).toBe(condition.flags)
    }
  })

  test('pattern condition without optional fields stays minimal in serialization', () => {
    const step: FlowStep = {
      action: 'if',
      condition: {
        type: 'pattern_match',
        source: 'query',
        pattern: 'test.*',
      } as any,
      then_steps: [{ action: 'return', status: '200' }],
    }

    const serialized = serializeDSL([step])
    // Should include required fields
    expect(serialized).toContain('"type":"pattern_match"')
    expect(serialized).toContain('"source":"query"')
    expect(serialized).toContain('"pattern":"test.*"')
    // Should NOT include optional fields
    expect(serialized).not.toContain('sourceKey')
    expect(serialized).not.toContain('strategy')
    expect(serialized).not.toContain('flags')
  })

  test('handles if step with string condition (non-pattern)', () => {
    const step: FlowStep = {
      action: 'if',
      condition: 'x > 5',
      then_steps: [{ action: 'return', status: '200' }],
    }

    const serialized = serializeDSL([step])
    // String conditions should remain as strings
    expect(serialized).toContain('if (x > 5) {')
  })

  test('handles if step with else_steps and pattern condition', () => {
    const step: FlowStep = {
      action: 'if',
      condition: {
        type: 'pattern_match',
        source: 'header',
        sourceKey: 'x-token',
        pattern: '[A-Z]+',
        flags: 'm',
      } as any,
      then_steps: [{ action: 'return', status: '200', body: 'success' }],
      else_steps: [{ action: 'return', status: '401', body: 'unauthorized' }],
    }

    const serialized = serializeDSL([step])
    expect(serialized).toContain('if ({')
    expect(serialized).toContain('"source":"header"')
    expect(serialized).toContain('} else {')
    expect(serialized).toContain('return(401')
  })

  test('handles nested patterns in flow steps', () => {
    const steps: FlowStep[] = [
      { action: 'set_const', value: 'hello', as: 'msg' },
      {
        action: 'if',
        condition: {
          type: 'pattern_match',
          source: 'header',
          sourceKey: 'content-type',
          pattern: 'application/json',
        } as any,
        then_steps: [
          { action: 'return', status: '200', body: 'json' },
        ],
      },
    ]

    const serialized = serializeDSL(steps)
    expect(serialized).toContain('msg = "hello"')
    expect(serialized).toContain('if ({')
    expect(serialized).toContain('"pattern":"application/json"')
  })
})

// Test suite 3: edge cases and special characters
describe('pattern condition edge cases', () => {
  test('pattern with asterisks and plus signs', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'body',
      pattern: 'user_*_info\\+meta',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.pattern).toBe('user_*_info\\+meta')
  })

  test('pattern with question mark and curly braces', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'path',
      pattern: '/api/v\\d{1,2}/.*',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.pattern).toBe('/api/v\\d{1,2}/.*')
  })

  test('pattern with pipe (OR) operator', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'query',
      pattern: '(prod|staging|dev)-(us|eu)',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.pattern).toBe('(prod|staging|dev)-(us|eu)')
  })

  test('pattern with caret and dollar anchors', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'header',
      sourceKey: 'x-api-key',
      pattern: '^[a-zA-Z0-9]{32}$',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.pattern).toBe('^[a-zA-Z0-9]{32}$')
  })

  test('all regex flags together', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'body',
      pattern: 'test',
      flags: 'imsx',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.flags).toBe('imsx')
  })

  test('sourceKey with special characters', () => {
    const cond: PatternCondition = {
      type: 'pattern_match',
      source: 'header',
      sourceKey: 'x-custom-header-123',
      pattern: '.*',
    }
    const serialized = serializePatternCondition(cond)
    expect(serialized.sourceKey).toBe('x-custom-header-123')
  })

  test('strategy values preserved when not auto', () => {
    const strategies: Array<'exact' | 'prefix' | 'suffix' | 'contains' | 'regex' | 'sequential'> = [
      'exact',
      'prefix',
      'suffix',
      'contains',
      'regex',
      'sequential',
    ]
    for (const strategy of strategies) {
      const cond: PatternCondition = {
        type: 'pattern_match',
        source: 'header',
        pattern: 'test',
        strategy,
      }
      const serialized = serializePatternCondition(cond)
      expect(serialized.strategy).toBe(strategy)
    }
  })
})

// Test suite 4: template pattern validate_pattern and extract round-trips
describe('validatePattern and extract round-trips', () => {
  test('validatePattern round-trip', () => {
    const dslText = `isValid = validatePattern(header.X-Custom, 'internal_*_suffix')`
    const parsed = parseDSL(dslText)
    expect(parsed).toHaveLength(1)
    const step = parsed[0] as any
    expect(step.action).toBe('validate_pattern')
    expect(step.source).toBe('header.X-Custom')
    expect(JSON.parse(step.input).pattern).toBe('internal_*_suffix')
    expect(step.as).toBe('isValid')

    const serialized = serializeDSL(parsed)
    expect(serialized).toContain('isValid = validatePattern(header.X-Custom,')
    expect(serialized).toContain('internal_*_suffix')
  })

  test('extract round-trip', () => {
    const dslText = `extract(header.X-Custom, 'internal_(service)_(env)_suffix')`
    const parsed = parseDSL(dslText)
    expect(parsed).toHaveLength(1)
    const step = parsed[0] as any
    expect(step.action).toBe('extract_pattern')
    expect(step.source).toBe('header.X-Custom')
    expect(JSON.parse(step.input).pattern).toBe('internal_(service)_(env)_suffix')
    expect(step.as).toBeUndefined()

    const serialized = serializeDSL(parsed)
    expect(serialized).toContain('extract(header.X-Custom,')
    expect(serialized).toContain('internal_(service)_(env)_suffix')
  })

  test('validatePattern with dynamic slot ref', () => {
    const dslText = `result = validatePattern(src, 'internal_*_{tenantId}')`
    const parsed = parseDSL(dslText)
    expect(parsed).toHaveLength(1)
    const step = parsed[0] as any
    const pattern = JSON.parse(step.input).pattern
    expect(pattern).toBe('internal_*_{tenantId}')

    const serialized = serializeDSL(parsed)
    expect(serialized).toContain('{tenantId}')
  })

  test('extract with multiple captures', () => {
    const dslText = `extract(header.Auth, 'Bearer_(token)_(exp)')`
    const parsed = parseDSL(dslText)
    expect(parsed).toHaveLength(1)
    const step = parsed[0] as any
    const pattern = JSON.parse(step.input).pattern
    expect(pattern).toContain('(token)')
    expect(pattern).toContain('(exp)')

    const serialized = serializeDSL(parsed)
    expect(serialized).toContain('Bearer_(token)_(exp)')
  })

  test('validatePattern serializes lhs', () => {
    const step: FlowStep = {
      action: 'validate_pattern',
      source: 'header.Content-Type',
      input: JSON.stringify({ pattern: 'application/json' }),
      as: 'isJson',
    }
    const serialized = serializeDSL([step])
    expect(serialized).toContain('isJson = validatePattern')
  })

  test('extract serializes without lhs', () => {
    const step: FlowStep = {
      action: 'extract_pattern',
      source: 'header.Auth',
      input: JSON.stringify({ pattern: 'Bearer_(token)' }),
    }
    const serialized = serializeDSL([step])
    expect(serialized).toContain('extract(header.Auth,')
    expect(serialized).not.toContain(' = extract')
  })
})

// Test helper function for running tests
function test(description: string, fn: () => void) {
  try {
    fn()
    console.log(`✓ ${description}`)
  } catch (e) {
    console.error(`✗ ${description}`)
    console.error(` `, e)
    throw e
  }
}

function expect(actual: any) {
  return {
    toBe(expected: any) {
      if (actual !== expected) throw new Error(`Expected ${expected}, got ${actual}`)
    },
    toEqual(expected: any) {
      if (!objectsEqual(actual, expected)) throw new Error(`Expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`)
    },
    toHaveLength(length: number) {
      if (actual.length !== length) throw new Error(`Expected length ${length}, got ${actual.length}`)
    },
    toContain(substring: string) {
      if (!actual.includes(substring)) throw new Error(`Expected to contain "${substring}", got "${actual}"`)
    },
    not: {
      toContain(substring: string) {
        if (actual.includes(substring)) throw new Error(`Expected NOT to contain "${substring}", got "${actual}"`)
      },
      toBeUndefined() {
        if (actual !== undefined) throw new Error(`Expected undefined, got ${actual}`)
      },
    },
    toBeUndefined() {
      if (actual !== undefined) throw new Error(`Expected undefined, got ${actual}`)
    },
    toBeDefined() {
      if (actual === undefined) throw new Error(`Expected defined, got undefined`)
    },
  }
}

function describe(name: string, fn: () => void) {
  console.log(`\n${name}`)
  fn()
}

// Export test helpers for use in other modules
export { test, expect, describe, objectsEqual }
