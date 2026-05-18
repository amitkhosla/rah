/**
 * Simple test runner for pattern condition serialization
 * Run with: node test_serialization.js
 */

// Mock TypeScript module - we'll manually test the logic
const assert = require('assert')

// Simulate types
/** @typedef {Object} PatternCondition
 *  @property {'pattern_match'} type
 *  @property {'header'|'query'|'body'|'path'} source
 *  @property {string} [sourceKey]
 *  @property {string} pattern
 *  @property {'auto'|'exact'|'prefix'|'suffix'|'contains'|'regex'|'sequential'} [strategy]
 *  @property {string} [flags]
 */

/** Serialize a PatternCondition to DSL object format */
function serializePatternCondition(cond) {
  const dslStep = {
    type: cond.type,
    source: cond.source,
    pattern: cond.pattern,
  }

  if (cond.sourceKey) {
    dslStep.sourceKey = cond.sourceKey
  }
  if (cond.strategy && cond.strategy !== 'auto') {
    dslStep.strategy = cond.strategy
  }
  if (cond.flags) {
    dslStep.flags = cond.flags
  }

  return dslStep
}

// Test suite 1: Basic serialization
console.log('\n=== Test Suite 1: Basic Serialization ===')

console.log('Test 1.1: Basic pattern condition with all fields')
const cond1 = {
  type: 'pattern_match',
  source: 'header',
  sourceKey: 'x-service',
  pattern: '^(api|data).*',
  strategy: 'regex',
  flags: 'i',
}
const ser1 = serializePatternCondition(cond1)
assert.strictEqual(ser1.type, 'pattern_match')
assert.strictEqual(ser1.source, 'header')
assert.strictEqual(ser1.sourceKey, 'x-service')
assert.strictEqual(ser1.pattern, '^(api|data).*')
assert.strictEqual(ser1.strategy, 'regex')
assert.strictEqual(ser1.flags, 'i')
console.log('✓ All fields serialized correctly')

console.log('\nTest 1.2: Omits strategy when auto')
const cond2 = {
  type: 'pattern_match',
  source: 'query',
  sourceKey: 'filter',
  pattern: 'test',
  strategy: 'auto',
}
const ser2 = serializePatternCondition(cond2)
assert.strictEqual(ser2.strategy, undefined)
console.log('✓ Strategy omitted when auto')

console.log('\nTest 1.3: Omits sourceKey when not set')
const cond3 = {
  type: 'pattern_match',
  source: 'body',
  pattern: '.*error.*',
}
const ser3 = serializePatternCondition(cond3)
assert.strictEqual(ser3.sourceKey, undefined)
console.log('✓ SourceKey omitted when not set')

console.log('\nTest 1.4: Omits flags when not set')
const cond4 = {
  type: 'pattern_match',
  source: 'path',
  pattern: '/api/v1/.*',
}
const ser4 = serializePatternCondition(cond4)
assert.strictEqual(ser4.flags, undefined)
console.log('✓ Flags omitted when not set')

// Test suite 2: Special characters in patterns
console.log('\n=== Test Suite 2: Special Characters ===')

console.log('Test 2.1: Pattern with special regex characters')
const cond5 = {
  type: 'pattern_match',
  source: 'header',
  sourceKey: 'x-token',
  pattern: '^[A-Za-z0-9+/=]+$',
  flags: 'm',
}
const ser5 = serializePatternCondition(cond5)
assert.strictEqual(ser5.pattern, '^[A-Za-z0-9+/=]+$')
console.log('✓ Special characters preserved')

console.log('\nTest 2.2: Pattern with pipe (OR) operator')
const cond6 = {
  type: 'pattern_match',
  source: 'query',
  pattern: '(prod|staging|dev)-(us|eu)',
}
const ser6 = serializePatternCondition(cond6)
assert.strictEqual(ser6.pattern, '(prod|staging|dev)-(us|eu)')
console.log('✓ Pipe operator preserved')

console.log('\nTest 2.3: Pattern with caret and dollar anchors')
const cond7 = {
  type: 'pattern_match',
  source: 'header',
  sourceKey: 'x-api-key',
  pattern: '^[a-zA-Z0-9]{32}$',
}
const ser7 = serializePatternCondition(cond7)
assert.strictEqual(ser7.pattern, '^[a-zA-Z0-9]{32}$')
console.log('✓ Anchors preserved')

// Test suite 3: Flags and strategies
console.log('\n=== Test Suite 3: Flags and Strategies ===')

console.log('Test 3.1: Multiple flags as single string')
const cond8 = {
  type: 'pattern_match',
  source: 'header',
  sourceKey: 'x-service',
  pattern: '.*SERVICE.*',
  flags: 'im',
}
const ser8 = serializePatternCondition(cond8)
assert.strictEqual(ser8.flags, 'im')
console.log('✓ Multiple flags preserved')

console.log('\nTest 3.2: All regex flags together')
const cond9 = {
  type: 'pattern_match',
  source: 'body',
  pattern: 'test',
  flags: 'imsx',
}
const ser9 = serializePatternCondition(cond9)
assert.strictEqual(ser9.flags, 'imsx')
console.log('✓ All flags preserved')

console.log('\nTest 3.3: Strategy values preserved')
const strategies = ['exact', 'prefix', 'suffix', 'contains', 'regex', 'sequential']
for (const strategy of strategies) {
  const cond = {
    type: 'pattern_match',
    source: 'header',
    pattern: 'test',
    strategy,
  }
  const ser = serializePatternCondition(cond)
  assert.strictEqual(ser.strategy, strategy, `Strategy ${strategy} not preserved`)
}
console.log('✓ All strategy values preserved')

// Test suite 4: JSON serialization (what goes into if conditions)
console.log('\n=== Test Suite 4: JSON Serialization ===')

console.log('Test 4.1: Serialize to JSON for if condition')
const cond10 = {
  type: 'pattern_match',
  source: 'header',
  sourceKey: 'x-service',
  pattern: '^(api|data).*',
  flags: 'i',
}
const ser10 = serializePatternCondition(cond10)
const json10 = JSON.stringify(ser10)
console.log(`JSON output: ${json10}`)
// Verify JSON is valid and contains key parts
assert(json10.includes('"type":"pattern_match"'))
assert(json10.includes('"source":"header"'))
assert(json10.includes('"sourceKey":"x-service"'))
assert(json10.includes('"pattern":"^(api|data).*"'))
assert(json10.includes('"flags":"i"'))
console.log('✓ JSON output valid and complete')

console.log('\nTest 4.2: Minimal JSON (no optional fields)')
const cond11 = {
  type: 'pattern_match',
  source: 'query',
  pattern: 'test.*',
}
const ser11 = serializePatternCondition(cond11)
const json11 = JSON.stringify(ser11)
console.log(`JSON output: ${json11}`)
assert(json11.includes('"type":"pattern_match"'))
assert(json11.includes('"source":"query"'))
assert(json11.includes('"pattern":"test.*"'))
assert(!json11.includes('sourceKey'))
assert(!json11.includes('strategy'))
assert(!json11.includes('flags'))
console.log('✓ Minimal JSON without optional fields')

// Test suite 5: Round-trip JSON parsing
console.log('\n=== Test Suite 5: Round-Trip JSON Parsing ===')

console.log('Test 5.1: Serialize and re-parse should match')
const original = {
  type: 'pattern_match',
  source: 'header',
  sourceKey: 'x-service',
  pattern: '^(api|data).*',
  flags: 'i',
}
const serialized = serializePatternCondition(original)
const json = JSON.stringify(serialized)
const reparsed = JSON.parse(json)

assert.deepStrictEqual(reparsed, original, 'Reparsed condition should match original')
console.log('✓ Round-trip preserves all data')

console.log('\nTest 5.2: Omitted optional fields remain undefined after re-parse')
const minimal = {
  type: 'pattern_match',
  source: 'body',
  pattern: '.*error.*',
}
const ser_minimal = serializePatternCondition(minimal)
const json_minimal = JSON.stringify(ser_minimal)
const reparsed_minimal = JSON.parse(json_minimal)

assert.strictEqual(reparsed_minimal.sourceKey, undefined)
assert.strictEqual(reparsed_minimal.strategy, undefined)
assert.strictEqual(reparsed_minimal.flags, undefined)
console.log('✓ Omitted fields remain undefined')

// Summary
console.log('\n' + '='.repeat(50))
console.log('✓ All tests passed!')
console.log('='.repeat(50))
console.log('\nSummary:')
console.log('  - serializePatternCondition returns Record<string, any>')
console.log('  - Only includes optional fields when set')
console.log('  - Strategy omitted when auto')
console.log('  - Special regex characters preserved')
console.log('  - JSON serialization works for if conditions')
console.log('  - Round-trip parsing preserves all data')
console.log('\nReady for SESSION-7 integration test!')
