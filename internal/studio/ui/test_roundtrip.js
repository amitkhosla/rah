/**
 * Integration test for pattern condition round-trip serialization
 * This tests: parse DSL → edit in UI → serialize back → parse again
 * Run with: node test_roundtrip.js
 */

const assert = require('assert')

// Simulate the serializePatternCondition function
function serializePatternCondition(cond) {
  const dslStep = {
    type: cond.type,
    source: cond.source,
    pattern: cond.pattern,
  }
  if (cond.sourceKey) dslStep.sourceKey = cond.sourceKey
  if (cond.strategy && cond.strategy !== 'auto') dslStep.strategy = cond.strategy
  if (cond.flags) dslStep.flags = cond.flags
  return dslStep
}

// Simulate if-step serialization (what serializeDSL does for 'if' action)
function serializeIfStepWithPattern(ifStep) {
  const condition = ifStep.condition
  let condStr = String(condition ?? '')

  // Check if condition is a PatternCondition object
  if (condition && typeof condition === 'object' && condition.type === 'pattern_match') {
    const patternObj = serializePatternCondition(condition)
    // Serialize to JSON representation for DSL output
    condStr = JSON.stringify(patternObj)
  }

  let dslLines = []
  dslLines.push(`if (${condStr}) {`)

  if (ifStep.then_steps && ifStep.then_steps.length > 0) {
    for (const step of ifStep.then_steps) {
      if (step.action === 'return') {
        const status = step.status || '200'
        const body = step.body || ''
        if (body) {
          dslLines.push(`  return(${status}, "${body}")`)
        } else {
          dslLines.push(`  return(${status})`)
        }
      }
    }
  }

  if (ifStep.else_steps && ifStep.else_steps.length > 0) {
    dslLines.push('} else {')
    for (const step of ifStep.else_steps) {
      if (step.action === 'return') {
        const status = step.status || '500'
        const body = step.body || ''
        if (body) {
          dslLines.push(`  return(${status}, "${body}")`)
        } else {
          dslLines.push(`  return(${status})`)
        }
      }
    }
  }

  dslLines.push('}')
  return dslLines.join('\n')
}

// Simulate parsePatternCondition from DSL
function parsePatternConditionFromObject(obj) {
  if (!obj || typeof obj !== 'object') return null
  if (obj.type === 'pattern_match') {
    return {
      type: 'pattern_match',
      source: obj.source || 'header',
      sourceKey: obj.sourceKey,
      pattern: obj.pattern || '',
      strategy: obj.strategy || 'auto',
      flags: obj.flags,
    }
  }
  return null
}

// Simulate parseIfStep from DSL text
function parseIfStepFromDSL(dslText) {
  // Extract condition from "if (condition) {"
  const match = dslText.match(/^if\s*\((.+)\)\s*\{/)
  if (!match) return null

  const condStr = match[1]

  // Try to parse as JSON (pattern condition)
  let condition = condStr
  try {
    const jsonObj = JSON.parse(condStr)
    const patternCond = parsePatternConditionFromObject(jsonObj)
    if (patternCond) {
      condition = patternCond
    }
  } catch (e) {
    // Not JSON, keep as string
  }

  // Parse body and else block
  const lines = dslText.split('\n')
  const thenSteps = []
  const elseSteps = []
  let inElse = false

  for (let i = 1; i < lines.length; i++) {
    const line = lines[i].trim()
    if (line === '} else {') {
      inElse = true
      continue
    }
    if (line === '}') break

    const retMatch = line.match(/^return\((\d+)(?:,\s*"([^"]*)")?\)$/)
    if (retMatch) {
      const step = {
        action: 'return',
        status: retMatch[1],
      }
      if (retMatch[2]) step.body = retMatch[2]

      if (inElse) {
        elseSteps.push(step)
      } else {
        thenSteps.push(step)
      }
    }
  }

  return {
    action: 'if',
    condition,
    then_steps: thenSteps,
    else_steps: elseSteps,
  }
}

console.log('\n=== Round-Trip Integration Test ===\n')

// Test 1: Full round-trip with pattern condition
console.log('Test 1: Full round-trip - Original → Serialize → Parse')

const originalIfStep = {
  action: 'if',
  condition: {
    type: 'pattern_match',
    source: 'header',
    sourceKey: 'x-service',
    pattern: '^(api|data).*',
    flags: 'i',
  },
  then_steps: [
    { action: 'return', status: '200', body: 'matched' },
  ],
  else_steps: [
    { action: 'return', status: '404', body: 'not_matched' },
  ],
}

console.log('Original condition:')
console.log(JSON.stringify(originalIfStep.condition, null, 2))

// Serialize to DSL
const dslText = serializeIfStepWithPattern(originalIfStep)
console.log('\nSerialized DSL:')
console.log(dslText)

// Parse back from DSL
const parsedIfStep = parseIfStepFromDSL(dslText)
console.log('\nParsed back condition:')
console.log(JSON.stringify(parsedIfStep.condition, null, 2))

// Verify round-trip
const originalCond = originalIfStep.condition
const parsedCond = parsedIfStep.condition

assert.strictEqual(originalCond.type, parsedCond.type, 'Type mismatch')
assert.strictEqual(originalCond.source, parsedCond.source, 'Source mismatch')
assert.strictEqual(originalCond.sourceKey, parsedCond.sourceKey, 'SourceKey mismatch')
assert.strictEqual(originalCond.pattern, parsedCond.pattern, 'Pattern mismatch')
assert.strictEqual(originalCond.flags, parsedCond.flags, 'Flags mismatch')
console.log('✓ Round-trip successful - conditions match')

// Test 2: Minimal pattern condition (no optional fields)
console.log('\n\nTest 2: Minimal pattern condition (no optional fields)')

const minimalStep = {
  action: 'if',
  condition: {
    type: 'pattern_match',
    source: 'query',
    pattern: 'test.*',
  },
  then_steps: [{ action: 'return', status: '200' }],
}

console.log('Original condition:')
console.log(JSON.stringify(minimalStep.condition, null, 2))

const minimalDSL = serializeIfStepWithPattern(minimalStep)
console.log('\nSerialized DSL:')
console.log(minimalDSL)

const parsedMinimal = parseIfStepFromDSL(minimalDSL)
console.log('\nParsed back condition:')
console.log(JSON.stringify(parsedMinimal.condition, null, 2))

const minOriginal = minimalStep.condition
const minParsed = parsedMinimal.condition

assert.strictEqual(minOriginal.type, minParsed.type)
assert.strictEqual(minOriginal.source, minParsed.source)
assert.strictEqual(minOriginal.pattern, minParsed.pattern)
// Optional fields should be undefined or default 'auto'
assert(minParsed.sourceKey === undefined || minParsed.sourceKey === null)
assert(minParsed.flags === undefined || minParsed.flags === null)
console.log('✓ Minimal condition round-trip successful')

// Test 3: Pattern with special characters
console.log('\n\nTest 3: Pattern with special regex characters')

const specialStep = {
  action: 'if',
  condition: {
    type: 'pattern_match',
    source: 'body',
    sourceKey: 'token',
    pattern: '^[A-Za-z0-9+/=]{32,}$',
    flags: 'ms',
  },
  then_steps: [{ action: 'return', status: '200', body: 'valid' }],
}

console.log('Original pattern:', specialStep.condition.pattern)

const specialDSL = serializeIfStepWithPattern(specialStep)
const parsedSpecial = parseIfStepFromDSL(specialDSL)

console.log('Parsed pattern:', parsedSpecial.condition.pattern)
assert.strictEqual(specialStep.condition.pattern, parsedSpecial.condition.pattern)
console.log('✓ Special characters preserved')

// Test 4: Multiple strategies
console.log('\n\nTest 4: Different strategy values')

const strategies = ['exact', 'prefix', 'suffix', 'contains', 'regex', 'sequential']

for (const strategy of strategies) {
  const stratStep = {
    action: 'if',
    condition: {
      type: 'pattern_match',
      source: 'header',
      pattern: 'test',
      strategy,
    },
    then_steps: [{ action: 'return', status: '200' }],
  }

  const stratDSL = serializeIfStepWithPattern(stratStep)
  const parsedStrat = parseIfStepFromDSL(stratDSL)

  assert.strictEqual(stratStep.condition.strategy, parsedStrat.condition.strategy)
}

console.log('✓ All strategy values round-trip correctly')

// Test 5: Verify JSON is valid in DSL
console.log('\n\nTest 5: JSON validity in DSL output')

const jsonStep = {
  action: 'if',
  condition: {
    type: 'pattern_match',
    source: 'header',
    sourceKey: 'x-api-key',
    pattern: 'secret-[0-9]+',
    flags: 'i',
  },
  then_steps: [{ action: 'return', status: '200' }],
}

const jsonDSL = serializeIfStepWithPattern(jsonStep)
const jsonMatch = jsonDSL.match(/^if\s*\((.+)\)\s*\{/)
assert(jsonMatch, 'Failed to extract condition from DSL')

const jsonStr = jsonMatch[1]
console.log('Condition string:', jsonStr)

// Verify it's valid JSON
let parsedJSON
try {
  parsedJSON = JSON.parse(jsonStr)
  console.log('✓ JSON is valid')
} catch (e) {
  throw new Error(`Invalid JSON in DSL: ${jsonStr}`)
}

assert.strictEqual(parsedJSON.type, 'pattern_match')
assert.strictEqual(parsedJSON.source, 'header')
assert.strictEqual(parsedJSON.sourceKey, 'x-api-key')
assert.strictEqual(parsedJSON.pattern, 'secret-[0-9]+')
assert.strictEqual(parsedJSON.flags, 'i')
console.log('✓ All JSON fields correct')

// Summary
console.log('\n' + '='.repeat(50))
console.log('✓ All round-trip tests passed!')
console.log('='.repeat(50))
console.log('\nSummary:')
console.log('  1. Full round-trip preserves all condition fields')
console.log('  2. Minimal conditions work without optional fields')
console.log('  3. Special regex characters preserved in patterns')
console.log('  4. Strategy values round-trip correctly')
console.log('  5. JSON output is valid for DSL parsing')
console.log('\nReady for SESSION-7 integration with actual dsl_parse.ts!')
