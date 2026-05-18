# RAH Studio UI — Detailed Session Plans

> **Purpose**: Each session below is self-contained and precise enough for any small model (even Gemma) to execute without mistakes. Every change is an exact FIND → REPLACE pair. No guessing, no inference, no extra changes beyond what's listed.
>
> **Budget tip**: Each session should be a fresh conversation. Use Haiku for single-file mechanical sessions (NEST-1a, NEST-1b). Use Sonnet for sessions touching multiple files or requiring logic (NAV-*, GRAPH-*).
>
> **Build command** (run after EVERY session):
> ```bash
> cd internal/studio/ui && npm run build
> ```
> Expected output: `✓ built in Xs` — zero TypeScript errors, zero warnings about missing exports.
>
> **Status**:
> - FIX-1: DONE (renderNestedStepCard content rendering fixed, token_validation defaults = signature,expiry)
> - NEST-1a: TODO
> - NEST-1b: TODO
> - NAV-1a: TODO
> - NAV-1b: DONE (breadcrumb bar rendering in FlowDesigner)
> - NAV-2: DONE (FlowMap onNavigate uses pushCurrent via onNavigateToFlow)
> - SUB-1: DONE
> - SUB-2: TODO
> - GRAPH-0: TODO
> - GRAPH-1: DONE

---

## SESSION NEST-1a — N-level branch helpers [Haiku OK]

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx` only.

**WHAT THIS DOES**: Adds a `BranchPath` type and a generic `setNestedStep` tree-updater function. Adds three new path-aware helper wrappers (`updateNestedStep`, `removeNestedStep`, `addToNestedBranch`) that accept a `BranchPath` instead of a fixed `parentIdx+branch`. The OLD helpers (`updateBranchStep`, `removeBranchStep`, `addToBranch`) are kept unchanged — they will be removed in NEST-1b.

**PREREQUISITE**: FIX-1 is complete.

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text in the file, it must be found:
```
function addToBranch(parentIdx: number, branch: 'then_steps' | 'else_steps', b: PaletteBlock) {
```

### CHANGE 1: Add BranchPath type after the imports block

**FIND** (exact text, including the blank line before):
```
// ── Visual Mode recipe definitions ───────────────────────────────
```

**REPLACE WITH**:
```
// ── Branch path type for n-level nesting ─────────────────────────
/**
 * Addresses a nested step inside the top-level steps array.
 * Example: [{ branch: 'then_steps', idx: 0 }, { branch: 'else_steps', idx: 2 }]
 * means steps[topIdx].then_steps[0].else_steps[2]
 */
export type BranchPath = Array<{ branch: 'then_steps' | 'else_steps'; idx: number }>

// ── Visual Mode recipe definitions ───────────────────────────────
```

### CHANGE 2: Add setNestedStep + new helpers after addToBranch

**FIND** (exact text):
```
  function handleSave() {
    if (!flowName.trim() || steps.length === 0) return
```

**REPLACE WITH**:
```
  // ── N-level tree updater ──────────────────────────────────────
  /**
   * Returns a new copy of `node` with the step at `path` replaced by `updater(step)`.
   * `path` is a BranchPath relative to `node`.
   * If path is empty, returns updater(node) directly.
   */
  function setNestedStep(
    node: FlowStep,
    path: BranchPath,
    updater: (s: FlowStep) => FlowStep | null,  // null = delete
  ): FlowStep {
    if (path.length === 0) return updater(node) ?? node
    const [head, ...rest] = path
    const arr = [...((node[head.branch] as FlowStep[]) ?? [])]
    if (rest.length === 0) {
      // At the target level
      const result = updater(arr[head.idx])
      if (result === null) {
        arr.splice(head.idx, 1)
      } else {
        arr[head.idx] = result
      }
    } else {
      arr[head.idx] = setNestedStep(arr[head.idx], rest, updater)
    }
    return { ...node, [head.branch]: arr }
  }

  /** Update a field on a step at arbitrary depth. topIdx = index in top-level steps[]. */
  function updateNestedStep(topIdx: number, path: BranchPath, key: string, value: unknown) {
    setSteps(steps.map((s, i) =>
      i !== topIdx ? s : setNestedStep(s, path, step => ({ ...step, [key]: value }))
    ))
  }

  /** Remove a step at arbitrary depth. */
  function removeNestedStep(topIdx: number, path: BranchPath) {
    setSteps(steps.map((s, i) =>
      i !== topIdx ? s : setNestedStep(s, path, () => null)
    ))
  }

  /** Append a new step to a branch at arbitrary depth. */
  function addToNestedBranch(topIdx: number, path: BranchPath, branch: 'then_steps' | 'else_steps', b: PaletteBlock) {
    setSteps(steps.map((s, i) => {
      if (i !== topIdx) return s
      // Navigate to the parent node, then append
      const navigate = (node: FlowStep, remaining: BranchPath): FlowStep => {
        if (remaining.length === 0) {
          const arr = [...((node[branch] as FlowStep[]) ?? []), { action: b.type, ...b.defaults }]
          return { ...node, [branch]: arr }
        }
        const [head, ...rest] = remaining
        const arr = [...((node[head.branch] as FlowStep[]) ?? [])]
        arr[head.idx] = navigate(arr[head.idx], rest)
        return { ...node, [head.branch]: arr }
      }
      return navigate(s, path)
    }))
  }

  function handleSave() {
    if (!flowName.trim() || steps.length === 0) return
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors. The new functions are not yet called so nothing changes visually.

### BROWSER TESTS
- Open the studio. It should load without a blank screen.
- The if/else editor should work as before (nothing changed in rendering yet).

### DO NOT TOUCH
- `updateBranchStep`, `removeBranchStep`, `addToBranch` — leave them in place.
- `renderNestedStepCard`, `renderBranch`, `renderIfBody` — do not change.
- Any file other than FlowDesigner.tsx.

---

## SESSION NEST-1b — Wire n-level rendering [Haiku OK]

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx` only.

**WHAT THIS DOES**: Updates `renderNestedStepCard` and `renderBranch` to accept a `BranchPath` parameter so they work at any depth. Nested if/else steps within a branch now recursively render their own THEN/ELSE sub-branches. Removes old `updateBranchStep`, `removeBranchStep`, `addToBranch` helpers.

**PREREQUISITE**: NEST-1a is complete and build passes.

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text, it must be found:
```
  function updateNestedStep(topIdx: number, path: BranchPath, key: string, value: unknown) {
```

Also search for this exact text, it must be found:
```
function renderNestedStepCard(step: FlowStep, ni: number, parentIdx: number, branch: 'then_steps' | 'else_steps') {
```

### CHANGE 1: Replace renderNestedStepCard with path-aware version

**FIND** (exact — lines 343-385 of current file):
```
  // ── Nested step card (inside if/else branches) ───────────────────
  function renderNestedStepCard(step: FlowStep, ni: number, parentIdx: number, branch: 'then_steps' | 'else_steps') {
    const key = `${parentIdx}-${branch}-${ni}`
    const isExp = nestedExpanded.has(key)
    const defs = fieldMap[step.action as string] ?? {}
    const updateFn = (k: string, v: string) => updateBranchStep(parentIdx, branch, ni, k, v)
    const fields = Object.entries(step).filter(([k]) => k !== 'action' && k !== 'then_steps' && k !== 'else_steps')
    return (
      <div key={key} style={{ margin: '4px 8px', borderRadius: 5, border: '1px solid rgba(255,255,255,0.07)', background: 'rgba(255,255,255,0.02)' }}>
        <div
          style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px', cursor: 'pointer', userSelect: 'none' }}
          onClick={() => setNestedExpanded(prev => {
            const s = new Set(prev)
            s.has(key) ? s.delete(key) : s.add(key)
            return s
          })}
        >
          <span style={{ fontSize: 10 }}>{isExp ? '▼' : '▶'}</span>
          <strong style={{ fontSize: 12, flex: 1 }}>{ni + 1}. {step.action as string}</strong>
          <button
            className="btn muted step-remove"
            style={{ fontSize: 11 }}
            onClick={e => { e.stopPropagation(); removeBranchStep(parentIdx, branch, ni) }}
          >×</button>
        </div>
        {isExp && (
          <div style={{ padding: '0 8px 8px' }}>
            {fields.map(([k, v]) => (
              <div key={k} className="field-row">
                <label className="field-label">{defs[k]?.label || k}</label>
                <input className="input"
                  placeholder={defs[k]?.placeholder || k}
                  value={String(v ?? '')}
                  onChange={e => updateFn(k, e.target.value)} />
              </div>
            ))}
            {fields.length === 0 && (
              <span style={{ fontSize: 11, color: 'var(--muted)' }}>No fields to configure.</span>
            )}
          </div>
        )}
      </div>
    )
  }
```

**REPLACE WITH**:
```
  // ── Nested step card (inside if/else branches, any depth) ───────────
  /**
   * topIdx    = index in the top-level steps[] array
   * path      = BranchPath from the top-level step down to (but not including) this step
   *             e.g. [{ branch: 'then_steps', idx: 0 }] means this step is inside steps[topIdx].then_steps[0]
   * ni        = index of this step within its immediate parent branch
   * branch    = which branch of the immediate parent ('then_steps' | 'else_steps')
   */
  function renderNestedStepCard(
    step: FlowStep,
    ni: number,
    topIdx: number,
    branch: 'then_steps' | 'else_steps',
    path: BranchPath,
  ) {
    const key = `${topIdx}-${path.map(p => `${p.branch}[${p.idx}]`).join('.')}-${branch}-${ni}`
    const isExp = nestedExpanded.has(key)
    const defs = fieldMap[step.action as string] ?? {}
    // Path to THIS step (used for update/remove)
    const stepPath: BranchPath = [...path, { branch, idx: ni }]
    const fields = Object.entries(step).filter(([k]) => k !== 'action' && k !== 'then_steps' && k !== 'else_steps')
    const isIf = step.action === 'if'

    return (
      <div key={key} style={{ margin: '4px 8px', borderRadius: 5, border: '1px solid rgba(255,255,255,0.07)', background: 'rgba(255,255,255,0.02)' }}>
        <div
          style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px', cursor: 'pointer', userSelect: 'none' }}
          onClick={() => setNestedExpanded(prev => {
            const s = new Set(prev)
            s.has(key) ? s.delete(key) : s.add(key)
            return s
          })}
        >
          <span style={{ fontSize: 10 }}>{isExp ? '▼' : '▶'}</span>
          <strong style={{ fontSize: 12, flex: 1 }}>{ni + 1}. {step.action as string}</strong>
          <button
            className="btn muted step-remove"
            style={{ fontSize: 11 }}
            onClick={e => { e.stopPropagation(); removeNestedStep(topIdx, stepPath) }}
          >×</button>
        </div>
        {isExp && (
          <div style={{ padding: '0 8px 8px' }}>
            {fields.map(([k, v]) => (
              <div key={k} className="field-row">
                <label className="field-label">{defs[k]?.label || k}</label>
                <input className="input"
                  placeholder={defs[k]?.placeholder || k}
                  value={String(v ?? '')}
                  onChange={e => updateNestedStep(topIdx, stepPath, k, e.target.value)} />
              </div>
            ))}
            {fields.length === 0 && !isIf && (
              <span style={{ fontSize: 11, color: 'var(--muted)' }}>No fields to configure.</span>
            )}
            {isIf && renderNestedBranches(step, topIdx, stepPath, defs)}
          </div>
        )}
      </div>
    )
  }

  /**
   * Renders the THEN/ELSE sub-branches for an `if` step that is itself nested.
   * stepPath = path to the `if` step itself (already includes its own branch+idx).
   */
  function renderNestedBranches(
    step: FlowStep,
    topIdx: number,
    stepPath: BranchPath,
    defs: Record<string, FieldDef>,
  ) {
    const thenSteps = (step.then_steps as FlowStep[]) ?? []
    const elseSteps = (step.else_steps as FlowStep[]) ?? []
    return (
      <>
        {thenSteps.length === 0 && (
          <div className="field-row">
            <label className="field-label">{defs['then']?.label ?? 'then'}</label>
            <input className="input" placeholder={defs['then']?.placeholder ?? 'then'}
              value={(step['then'] as string) ?? ''}
              onChange={e => updateNestedStep(topIdx, stepPath, 'then', e.target.value)} />
          </div>
        )}
        {renderBranch('✓ THEN', false, thenSteps, topIdx, 'then_steps', stepPath)}
        {elseSteps.length === 0 && (
          <div className="field-row">
            <label className="field-label">{defs['else']?.label ?? 'else'}</label>
            <input className="input" placeholder={defs['else']?.placeholder ?? 'else'}
              value={(step['else'] as string) ?? ''}
              onChange={e => updateNestedStep(topIdx, stepPath, 'else', e.target.value)} />
          </div>
        )}
        {renderBranch('✗ ELSE', true, elseSteps, topIdx, 'else_steps', stepPath)}
      </>
    )
  }
```

### CHANGE 2: Replace renderBranch with path-aware version

**FIND** (exact):
```
  // ── Inline branch drop zone (then/else) ───────────────────────────
  function renderBranch(label: string, isElse: boolean, branchSteps: FlowStep[], parentIdx: number, branch: 'then_steps' | 'else_steps') {
    const branchKey = `${parentIdx}-${branch}`
    const isDragOver = dragOverBranch === branchKey
    const borderColor = isElse ? '#ef4444' : '#22c55e'
    const labelColor  = isElse ? '#ef4444' : '#22c55e'

    return (
      <div style={{ marginTop: 8, borderRadius: 6, border: '1px solid rgba(255,255,255,0.08)', borderLeft: `3px solid ${borderColor}` }}>
        <div style={{ fontSize: 11, fontWeight: 700, padding: '4px 10px', color: labelColor, background: 'rgba(255,255,255,0.03)', letterSpacing: '0.06em' }}>
          {label}
        </div>
        {branchSteps.map((ns, ni) => renderNestedStepCard(ns, ni, parentIdx, branch))}
        <div
          style={{
            margin: '6px 8px',
            padding: '7px 10px',
            border: `1.5px dashed ${isDragOver ? borderColor : 'rgba(255,255,255,0.15)'}`,
            borderRadius: 5,
            fontSize: 11,
            color: isDragOver ? borderColor : 'var(--muted)',
            background: isDragOver ? `${borderColor}10` : 'transparent',
            cursor: 'default',
            textAlign: 'center' as const,
            transition: 'border-color 0.15s, background 0.15s, color 0.15s',
          }}
          onDragOver={e => { e.preventDefault(); e.stopPropagation(); setDragOverBranch(branchKey) }}
          onDragLeave={e => { e.stopPropagation(); setDragOverBranch(null) }}
          onDrop={e => {
            e.preventDefault()
            e.stopPropagation()  // prevents canvas handleDrop from firing
            setDragOverBranch(null)
            const raw = e.dataTransfer.getData('application/json')
            if (!raw) return
            const b: PaletteBlock = JSON.parse(raw) as PaletteBlock
            addToBranch(parentIdx, branch, b)
          }}
        >
          {branchSteps.length === 0 ? '+ Drop step here' : '+ Drop another step'}
        </div>
      </div>
    )
  }
```

**REPLACE WITH**:
```
  // ── Inline branch drop zone (then/else, any depth) ───────────────────
  /**
   * topIdx   = index in the top-level steps[] array (never changes as we recurse)
   * branch   = 'then_steps' | 'else_steps' of the immediate parent
   * parentPath = BranchPath to the parent `if` step (empty [] for top-level if steps)
   */
  function renderBranch(
    label: string,
    isElse: boolean,
    branchSteps: FlowStep[],
    topIdx: number,
    branch: 'then_steps' | 'else_steps',
    parentPath: BranchPath = [],
  ) {
    const branchKey = `${topIdx}-${parentPath.map(p => `${p.branch}[${p.idx}]`).join('.')}-${branch}`
    const isDragOver = dragOverBranch === branchKey
    const borderColor = isElse ? '#ef4444' : '#22c55e'
    const labelColor  = isElse ? '#ef4444' : '#22c55e'

    return (
      <div style={{ marginTop: 8, borderRadius: 6, border: '1px solid rgba(255,255,255,0.08)', borderLeft: `3px solid ${borderColor}` }}>
        <div style={{ fontSize: 11, fontWeight: 700, padding: '4px 10px', color: labelColor, background: 'rgba(255,255,255,0.03)', letterSpacing: '0.06em' }}>
          {label}
        </div>
        {branchSteps.map((ns, ni) => renderNestedStepCard(ns, ni, topIdx, branch, parentPath))}
        <div
          style={{
            margin: '6px 8px',
            padding: '7px 10px',
            border: `1.5px dashed ${isDragOver ? borderColor : 'rgba(255,255,255,0.15)'}`,
            borderRadius: 5,
            fontSize: 11,
            color: isDragOver ? borderColor : 'var(--muted)',
            background: isDragOver ? `${borderColor}10` : 'transparent',
            cursor: 'default',
            textAlign: 'center' as const,
            transition: 'border-color 0.15s, background 0.15s, color 0.15s',
          }}
          onDragOver={e => { e.preventDefault(); e.stopPropagation(); setDragOverBranch(branchKey) }}
          onDragLeave={e => { e.stopPropagation(); setDragOverBranch(null) }}
          onDrop={e => {
            e.preventDefault()
            e.stopPropagation()
            setDragOverBranch(null)
            const raw = e.dataTransfer.getData('application/json')
            if (!raw) return
            const b: PaletteBlock = JSON.parse(raw) as PaletteBlock
            addToNestedBranch(topIdx, parentPath, branch, b)
          }}
        >
          {branchSteps.length === 0 ? '+ Drop step here' : '+ Drop another step'}
        </div>
      </div>
    )
  }
```

### CHANGE 3: Update renderIfBody to pass empty parentPath to renderBranch

**FIND** (exact):
```
  // ── Special step bodies ─────────────────────────────────────────
  function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const thenSteps = (step.then_steps as FlowStep[]) ?? []
    const elseSteps = (step.else_steps as FlowStep[]) ?? []
    return (
      <div className="step-body">
        {fieldInput(i, 'condition', (step['condition'] as string) ?? '', defs['condition'], { smart: true })}
        {thenSteps.length === 0 && fieldInput(i, 'then', (step['then'] as string) ?? '', defs['then'])}
        {renderBranch('✓ THEN', false, thenSteps, i, 'then_steps')}
        {elseSteps.length === 0 && fieldInput(i, 'else', (step['else'] as string) ?? '', defs['else'])}
        {renderBranch('✗ ELSE', true, elseSteps, i, 'else_steps')}
      </div>
    )
  }
```

**REPLACE WITH**:
```
  // ── Special step bodies ─────────────────────────────────────────
  function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const thenSteps = (step.then_steps as FlowStep[]) ?? []
    const elseSteps = (step.else_steps as FlowStep[]) ?? []
    return (
      <div className="step-body">
        {fieldInput(i, 'condition', (step['condition'] as string) ?? '', defs['condition'], { smart: true })}
        {thenSteps.length === 0 && fieldInput(i, 'then', (step['then'] as string) ?? '', defs['then'])}
        {renderBranch('✓ THEN', false, thenSteps, i, 'then_steps', [])}
        {elseSteps.length === 0 && fieldInput(i, 'else', (step['else'] as string) ?? '', defs['else'])}
        {renderBranch('✗ ELSE', true, elseSteps, i, 'else_steps', [])}
      </div>
    )
  }
```

### CHANGE 4: Remove old shallow helpers (updateBranchStep, removeBranchStep, addToBranch)

**FIND** (exact):
```
  function updateBranchStep(parentIdx: number, branch: 'then_steps' | 'else_steps', ni: number, key: string, value: unknown) {
    setSteps(steps.map((s, idx) => {
      if (idx !== parentIdx) return s
      const arr = [...((s[branch] as FlowStep[]) ?? [])]
      arr[ni] = { ...arr[ni], [key]: value }
      return { ...s, [branch]: arr }
    }))
  }

  function removeBranchStep(parentIdx: number, branch: 'then_steps' | 'else_steps', ni: number) {
    setSteps(steps.map((s, idx) => {
      if (idx !== parentIdx) return s
      const arr = ((s[branch] as FlowStep[]) ?? []).filter((_, j) => j !== ni)
      return { ...s, [branch]: arr }
    }))
  }

  function addToBranch(parentIdx: number, branch: 'then_steps' | 'else_steps', b: PaletteBlock) {
    setSteps(steps.map((s, idx) => {
      if (idx !== parentIdx) return s
      const arr = [...((s[branch] as FlowStep[]) ?? []), { action: b.type, ...b.defaults }]
      return { ...s, [branch]: arr }
    }))
  }
```

**REPLACE WITH**: *(empty — delete these three functions entirely)*
```
```

> **NOTE to model**: The REPLACE WITH block above is intentionally empty. You are deleting those three functions. After the deletion, the line that was before `updateBranchStep` should be directly followed by the line that was after `addToBranch` (which is a blank line then `function handleSave`).

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors.

### BROWSER TESTS
1. Open studio → Flow Designer.
2. Add an `if` step. Expand it. Drag a `cache_get` block into the THEN branch drop zone → it should appear.
3. Click the `cache_get` card in THEN → arrow toggles ▶/▼, fields appear when expanded.
4. Drag another `if` step into the THEN branch. Expand it. It should show its own THEN/ELSE sub-branches inside.
5. Drag a step into the nested `if`'s THEN → should work.
6. Click × on a nested step → it should be removed.

### DO NOT TOUCH
- Any file other than FlowDesigner.tsx.
- `fieldInput`, `renderSwitchBody`, `handleSave`, state declarations, or anything after line ~450.

---

## SESSION NAV-1a — Flow navigation stack in App.tsx [Sonnet]

**FILE**: `internal/studio/ui/src/App.tsx` only.

**WHAT THIS DOES**: Adds a `navStack` state to App to track flow navigation history. Updates `navigateToDesigner` to push/replace entries on this stack. Passes `navStack` and `onNavigateBack` down to FlowDesigner as new props.

**PREREQUISITE**: NEST-1b is complete and build passes.

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text, it must be found:
```
  function navigateToDesigner(name?: string) {
    if (name) {
      const saved = savedFlows.find(f => f.name === name)
      setFlowName(name)
      setSteps(saved?.steps ?? [])
    } else {
      setFlowName('')
      setSteps([])
    }
    setTab('flows')
  }
```

### CHANGE 1: Add navStack state after the flowName/steps state

**FIND** (exact):
```
  // Active flow in Flow Designer
  const [flowName, setFlowName] = useState('')
  const [steps,    setSteps]    = useState<FlowStep[]>([])
```

**REPLACE WITH**:
```
  // Active flow in Flow Designer
  const [flowName, setFlowName] = useState('')
  const [steps,    setSteps]    = useState<FlowStep[]>([])

  // Navigation stack: each entry is the flow name we navigated FROM (breadcrumb trail)
  // e.g. ['root_flow', 'auth_flow'] means we drilled: root_flow → auth_flow → current
  const [navStack, setNavStack] = useState<string[]>([])
```

### CHANGE 2: Replace navigateToDesigner with stack-aware version

**FIND** (exact):
```
  // Navigate to designer, optionally loading a named flow
  function navigateToDesigner(name?: string) {
    if (name) {
      const saved = savedFlows.find(f => f.name === name)
      setFlowName(name)
      setSteps(saved?.steps ?? [])
    } else {
      setFlowName('')
      setSteps([])
    }
    setTab('flows')
  }
```

**REPLACE WITH**:
```
  // Navigate to designer, optionally loading a named flow.
  // If `pushCurrent` is true, the current flowName is pushed onto the navStack first
  // (used when drilling into a sub-flow from FlowMap or a call step).
  function navigateToDesigner(name?: string, pushCurrent?: boolean) {
    if (name) {
      const saved = savedFlows.find(f => f.name === name)
      if (pushCurrent && flowName) {
        setNavStack(prev => [...prev, flowName])
      } else if (!pushCurrent) {
        setNavStack([])
      }
      setFlowName(name)
      setSteps(saved?.steps ?? [])
    } else {
      setFlowName('')
      setSteps([])
      setNavStack([])
    }
    setTab('flows')
  }

  // Navigate back to a specific stack index (0 = root).
  // Pops everything above that index.
  function navigateToStackIndex(idx: number) {
    const target = navStack[idx]
    const saved = savedFlows.find(f => f.name === target)
    setFlowName(target)
    setSteps(saved?.steps ?? [])
    setNavStack(prev => prev.slice(0, idx))
    setTab('flows')
  }
```

### CHANGE 3: Pass navStack/onNavigateBack to FlowDesigner JSX

**FIND** (exact):
```
          <FlowDesigner
            blocks={blocks}
            steps={steps}
            setSteps={setSteps}
            flowName={flowName}
            setFlowName={setFlowName}
            savedFlows={savedFlows}
            onSaveFlow={() => saveCurrentFlow([], {})}
            onNavigateToFlow={navigateToDesigner}
          />
```

**REPLACE WITH**:
```
          <FlowDesigner
            blocks={blocks}
            steps={steps}
            setSteps={setSteps}
            flowName={flowName}
            setFlowName={setFlowName}
            savedFlows={savedFlows}
            onSaveFlow={() => saveCurrentFlow([], {})}
            onNavigateToFlow={(name) => navigateToDesigner(name, true)}
            navStack={navStack}
            onNavigateBack={navigateToStackIndex}
          />
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: TypeScript errors for `navStack` and `onNavigateBack` not in FlowDesigner Props — that is OK and expected. It will be fixed in NAV-1b.

Actually, wait — this will fail. To avoid TS errors, we need to add the props to FlowDesigner's Props interface at the same time. So this session must also update FlowDesigner.tsx Props interface.

**CHANGE 4: Add navStack and onNavigateBack to FlowDesigner Props interface**

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx`

**FIND** (exact):
```
interface Props {
  blocks: PaletteBlock[]
  steps: FlowStep[]
  setSteps: (steps: FlowStep[]) => void
  flowName: string
  setFlowName: (name: string) => void
  savedFlows: SavedFlow[]
  onSaveFlow: () => void
  onNavigateToFlow?: (name: string) => void
}
```

**REPLACE WITH**:
```
interface Props {
  blocks: PaletteBlock[]
  steps: FlowStep[]
  setSteps: (steps: FlowStep[]) => void
  flowName: string
  setFlowName: (name: string) => void
  savedFlows: SavedFlow[]
  onSaveFlow: () => void
  onNavigateToFlow?: (name: string) => void
  /** Ordered list of flow names we navigated from (breadcrumb trail, excluding current). */
  navStack?: string[]
  /** Jump back to navStack[idx], popping everything above it. */
  onNavigateBack?: (idx: number) => void
}
```

**CHANGE 5: Destructure new props in FlowDesigner component**

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx`

**FIND** (exact):
```
export default function FlowDesigner({
  blocks, steps, setSteps, flowName, setFlowName, savedFlows, onSaveFlow, onNavigateToFlow,
}: Props) {
```

**REPLACE WITH**:
```
export default function FlowDesigner({
  blocks, steps, setSteps, flowName, setFlowName, savedFlows, onSaveFlow, onNavigateToFlow,
  navStack = [], onNavigateBack,
}: Props) {
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors. New props accepted but not yet rendered.

### BROWSER TESTS
- Studio loads, flow designer works as before. No visual change yet.

### DO NOT TOUCH
- Any component other than FlowDesigner and App.
- The APIsSection call site `onNavigateToDesigner={navigateToDesigner}` — leave it unchanged.

---

## SESSION NAV-1b — Breadcrumb bar in FlowDesigner [Haiku OK]

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx` only.

**WHAT THIS DOES**: Renders a breadcrumb bar at the top of the designer when `navStack` has entries. Each crumb is clickable and calls `onNavigateBack(idx)`. Current flow name is shown as the last (non-clickable) crumb.

**PREREQUISITE**: NAV-1a is complete and build passes.

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text, it must be found:
```
  navStack = [], onNavigateBack,
}: Props) {
```

### CHANGE 1: Add breadcrumb rendering just before the flow name input

**FIND** (exact — in the JSX render area):
```
          <input
            className="input"
            placeholder="flow name (required to save)"
            value={flowName}
            onChange={e => setFlowName(e.target.value)}
          />
```

**REPLACE WITH**:
```
          {navStack.length > 0 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 0 2px', fontSize: 12, flexWrap: 'wrap' }}>
              {navStack.map((name, idx) => (
                <span key={idx} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <button
                    className="btn muted"
                    style={{ fontSize: 12, padding: '1px 6px', borderRadius: 4 }}
                    onClick={() => onNavigateBack?.(idx)}
                  >
                    {name}
                  </button>
                  <span style={{ color: 'var(--muted)', fontSize: 10 }}>›</span>
                </span>
              ))}
              <span style={{ fontSize: 12, color: 'var(--fg)', fontWeight: 600 }}>{flowName}</span>
            </div>
          )}
          <input
            className="input"
            placeholder="flow name (required to save)"
            value={flowName}
            onChange={e => setFlowName(e.target.value)}
          />
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors.

### BROWSER TESTS
1. Open studio. Create two flows: `root_flow` and `auth_flow`.
2. In `root_flow`, add a `call` step with `flow_name = auth_flow`. Save.
3. Open FlowMap sidebar (the tree icon). Click `auth_flow` link — should navigate to `auth_flow` with breadcrumb: `root_flow ›  auth_flow`.
4. Click `root_flow` breadcrumb → should navigate back to `root_flow` with no breadcrumb shown.

### DO NOT TOUCH
- No other files. No styling files.

---

## SESSION NAV-2 — FlowMap navigate uses pushCurrent [Haiku OK]

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx` only.

**WHAT THIS DOES**: The FlowMap sidebar currently calls `onNavigateToFlow(name)` directly. We need it to push the current flow onto the navStack before navigating. This is done by updating the `onNavigate` prop passed to `<FlowMap>` in FlowDesigner.

**PREREQUISITE**: NAV-1b is complete and build passes.

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text, it must be found:
```
              onNavigate={name => {
                if (onNavigateToFlow) onNavigateToFlow(name)
              }}
```

### CHANGE 1: FlowMap onNavigate uses the prop directly (which now pushes)

The `onNavigateToFlow` prop in App.tsx already wraps the call with `pushCurrent=true` (done in NAV-1a, CHANGE 3). So the only change needed is to ensure FlowDesigner passes through the call correctly.

**FIND** (exact):
```
              onNavigate={name => {
                if (onNavigateToFlow) onNavigateToFlow(name)
              }}
```

**REPLACE WITH**:
```
              onNavigate={name => onNavigateToFlow?.(name)}
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors.

### BROWSER TESTS
- Same as NAV-1b test: clicking a flow in FlowMap should push the current flow onto the breadcrumb and navigate to the target.

### DO NOT TOUCH
- No other files.

---

## SESSION SUB-1 — Sub-flow selection UI in FlowDesigner [Sonnet]

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx` only.

**WHAT THIS DOES**: Adds a "selection mode" to the FlowDesigner canvas. When the user clicks the "Extract Sub-flow" button (new button in toolbar), each step card gets a checkbox. The user selects steps, types a name, and clicks "Extract". The selected steps are removed from the current flow and saved as a new flow; a `call` step referencing the new flow is inserted at the position of the first selected step.

**PREREQUISITE**: NAV-1b complete and build passes.

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text, it must be found:
```
  const [showFlowMap, setShowFlowMap]   = useState(false)
```

### CHANGE 1: Add selection state

**FIND** (exact):
```
  const [showFlowMap, setShowFlowMap]   = useState(false)
```

**REPLACE WITH**:
```
  const [showFlowMap, setShowFlowMap]   = useState(false)
  const [selectMode, setSelectMode]     = useState(false)
  const [selectedSteps, setSelectedSteps] = useState<Set<number>>(new Set())
  const [extractName, setExtractName]   = useState('')
```

### CHANGE 2: Add extractSubFlow function (after handleSave)

**FIND** (exact):
```
  // ── Field input with source-ref chips ──────────────────────────
  function fieldInput(
```

**REPLACE WITH**:
```
  function extractSubFlow() {
    if (!extractName.trim() || selectedSteps.size === 0) return
    const sorted = [...selectedSteps].sort((a, b) => a - b)
    const subSteps = sorted.map(i => steps[i])
    // Save the sub-flow
    onSaveFlow  // We can't call onSaveFlow directly with different steps; use the prop
    // We need the parent (App) to save, but we only have onSaveFlow which saves current steps.
    // Instead, we fire the navigate callback with a sentinel to create a new named flow.
    // For now: persist to localStorage directly under savedFlows key, then reload.
    // This is the simplest approach that does not require a new prop.
    const raw = localStorage.getItem('rah_studio_v1')
    const snap = raw ? JSON.parse(raw) as { savedFlows?: Array<{ name: string; steps: FlowStep[] }> } : {}
    const existingFlows: Array<{ name: string; steps: FlowStep[] }> = snap.savedFlows ?? []
    const already = existingFlows.find(f => f.name === extractName.trim())
    if (!already) {
      existingFlows.push({ name: extractName.trim(), steps: subSteps })
      localStorage.setItem('rah_studio_v1', JSON.stringify({ ...snap, savedFlows: existingFlows }))
    }
    // Replace selected steps with a single call step
    const firstIdx = sorted[0]
    const newSteps = steps.filter((_, i) => !selectedSteps.has(i))
    const callStep: FlowStep = { action: 'call', flow_name: extractName.trim() }
    newSteps.splice(firstIdx, 0, callStep)
    setSteps(newSteps)
    setSelectMode(false)
    setSelectedSteps(new Set())
    setExtractName('')
    // Reload savedFlows from localStorage (App will re-read on next render cycle via its own useEffect)
    window.dispatchEvent(new StorageEvent('storage', { key: 'rah_studio_v1' }))
  }

  // ── Field input with source-ref chips ──────────────────────────
  function fieldInput(
```

### CHANGE 3: Add "Extract Sub-flow" button and extraction UI to the toolbar

Find the toolbar area. Search for this exact text:

**FIND** (exact):
```
          <button
            className="btn muted"
            style={{ fontSize: 13 }}
            onClick={() => setShowFlowMap(p => !p)}
            title="Toggle flow dependency map"
          >
```

**REPLACE WITH**:
```
          {selectMode && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <input
                className="input"
                placeholder="new sub-flow name"
                value={extractName}
                onChange={e => setExtractName(e.target.value)}
                style={{ width: 160 }}
              />
              <button
                className="btn accent"
                style={{ fontSize: 12 }}
                onClick={extractSubFlow}
                disabled={selectedSteps.size === 0 || !extractName.trim()}
              >
                Extract ({selectedSteps.size})
              </button>
              <button
                className="btn muted"
                style={{ fontSize: 12 }}
                onClick={() => { setSelectMode(false); setSelectedSteps(new Set()); setExtractName('') }}
              >
                Cancel
              </button>
            </div>
          )}
          {!selectMode && (
            <button
              className="btn muted"
              style={{ fontSize: 12 }}
              onClick={() => setSelectMode(true)}
              title="Select steps to extract as a sub-flow"
            >
              ⊡ Extract
            </button>
          )}
          <button
            className="btn muted"
            style={{ fontSize: 13 }}
            onClick={() => setShowFlowMap(p => !p)}
            title="Toggle flow dependency map"
          >
```

### CHANGE 4: Add checkbox to each top-level step card header in select mode

This requires finding the step card header render. Search for the step's drag handle / expand button row.

**FIND** (exact):
```
                onClick={() => setExpanded(prev => {
                    const next = new Set(prev)
                    next.has(i) ? next.delete(i) : next.add(i)
                    return next
                  })}
```

> **NOTE**: If this exact string is not found (indentation may differ), search for `next.has(i) ? next.delete(i) : next.add(i)` and find the enclosing `onClick` handler — that is the step header click. Add the checkbox immediately before the drag handle span inside that row.

Actually, finding the right exact location for step checkboxes is complex and depends on exact indentation. **Skip CHANGE 4** for this session. The extract button + name input (CHANGES 1-3) are the core functionality. The checkbox selection can be done in session SUB-2.

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors.

### BROWSER TESTS
- Studio loads, no visual changes yet (extract button visible but select mode off by default).
- Click "⊡ Extract" button — extraction UI (name input + buttons) should appear.
- Click "Cancel" — returns to normal.

### DO NOT TOUCH
- No other files.

---

## SESSION SUB-2 — Step checkboxes for sub-flow extraction [Haiku OK]

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx` only.

**WHAT THIS DOES**: When `selectMode` is true, renders a checkbox to the left of each top-level step card. Clicking the checkbox toggles the step's membership in `selectedSteps`.

**PREREQUISITE**: SUB-1 complete and build passes.

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text, it must be found:
```
  const [selectMode, setSelectMode]     = useState(false)
  const [selectedSteps, setSelectedSteps] = useState<Set<number>>(new Set())
```

### CHANGE 1: Find the top-level step card wrapper and add checkbox

Look for the step card's outermost container. Search for this exact string:

**FIND** (exact):
```
              draggable
              onDragStart={e => {
                e.dataTransfer.effectAllowed = 'move'
                e.dataTransfer.setData('step-reorder', String(i))
              }}
```

Check the 3-4 lines before this — it should be the `<div` that opens the step card. The checkbox needs to go inside this container, as the first child, shown only when `selectMode` is true.

Actually, to avoid indentation guessing, here is a safer approach:

**FIND** (exact — the step card body expand toggle area, which is always unique per card):
```
              onClick={() => setExpanded(prev => {
```

The line immediately above this `onClick` should be a `style` prop or similar. We want to add a checkbox BEFORE the step card `<div`. 

This approach is risky without exact line context. **Recommended**: use the Explore agent first to get exact lines 600-700 of FlowDesigner.tsx, then provide exact FIND/REPLACE for SUB-2. Do that before executing SUB-2.

### PREREQUISITE FOR THIS SESSION
Run: Read FlowDesigner.tsx lines 580-720 to get the exact step card render code, then fill in the exact FIND/REPLACE below.

### BUILD
```bash
cd internal/studio/ui && npm run build
```

### DO NOT TOUCH
- No other files.

---

## SESSION GRAPH-0 — Export helpers from FlowMap.tsx [Haiku OK]

**FILE**: `internal/studio/ui/src/components/FlowMap.tsx` only.

**WHAT THIS DOES**: Changes `collectFlowRefs` and `buildCallGraph` from private functions to exported functions so they can be imported by a future graph view component.

**PREREQUISITE**: NAV-1b complete (or can be done independently — no dependency on NEST sessions).

**VERIFY FILE STATE BEFORE STARTING** — search for this exact text, it must be found:
```
function collectFlowRefs(steps: FlowStep[]): string[] {
```

And also search for:
```
function buildCallGraph(flows: SavedFlow[]): Map<string, string[]> {
```

### CHANGE 1: Export collectFlowRefs

**FIND** (exact):
```
function collectFlowRefs(steps: FlowStep[]): string[] {
```

**REPLACE WITH**:
```
export function collectFlowRefs(steps: FlowStep[]): string[] {
```

### CHANGE 2: Export buildCallGraph

**FIND** (exact):
```
function buildCallGraph(flows: SavedFlow[]): Map<string, string[]> {
```

**REPLACE WITH**:
```
export function buildCallGraph(flows: SavedFlow[]): Map<string, string[]> {
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors. No visual change.

### BROWSER TESTS
- Studio loads normally. FlowMap sidebar still works.

### DO NOT TOUCH
- No other files.
- The rest of FlowMap.tsx (FlowMapNode, default export FlowMap) — do not change.

---

## SESSION GRAPH-1 — Full graph view component [Sonnet]

**FILES**: 
1. Create NEW file: `internal/studio/ui/src/components/FlowGraph.tsx`
2. Modify: `internal/studio/ui/src/components/FlowDesigner.tsx` (import + tab toggle)

**WHAT THIS DOES**: Adds a simple DAG visualization tab alongside the FlowMap. Uses SVG to render nodes (flow names) as rectangles and directed edges (call/then/else references) as arrows. No external dependency — pure SVG.

**PREREQUISITE**: GRAPH-0 complete and build passes.

**VERIFY FILE STATE BEFORE STARTING**:
- Search FlowMap.tsx for `export function buildCallGraph` — must be found.
- Search FlowDesigner.tsx for `import FlowMap from './FlowMap'` — must be found.

### CHANGE 1: Create FlowGraph.tsx

Create the file `internal/studio/ui/src/components/FlowGraph.tsx` with this exact content:

```tsx
import { useMemo } from 'react'
import type { SavedFlow } from '../types'
import { buildCallGraph } from './FlowMap'

interface Props {
  savedFlows: SavedFlow[]
  currentFlow: string
  onNavigate: (name: string) => void
}

const NODE_W = 120
const NODE_H = 36
const H_GAP  = 60
const V_GAP  = 24

interface LayoutNode {
  name: string
  x: number
  y: number
  col: number
  row: number
}

function layoutGraph(flows: SavedFlow[], graph: Map<string, string[]>): LayoutNode[] {
  const names = flows.map(f => f.name)
  // Assign columns by BFS depth from nodes with no incoming edges
  const inDegree = new Map<string, number>()
  names.forEach(n => inDegree.set(n, 0))
  graph.forEach((children) => {
    children.forEach(c => { if (names.includes(c)) inDegree.set(c, (inDegree.get(c) ?? 0) + 1) })
  })
  const cols = new Map<string, number>()
  const queue = names.filter(n => (inDegree.get(n) ?? 0) === 0)
  queue.forEach(n => cols.set(n, 0))
  let head = 0
  while (head < queue.length) {
    const n = queue[head++]
    const col = cols.get(n) ?? 0
    ;(graph.get(n) ?? []).filter(c => names.includes(c)).forEach(c => {
      const cur = cols.get(c) ?? 0
      if (cur <= col) {
        cols.set(c, col + 1)
        queue.push(c)
      }
    })
  }
  // Nodes not reached
  names.forEach(n => { if (!cols.has(n)) cols.set(n, 0) })

  // Assign rows within each column
  const byCol = new Map<number, string[]>()
  names.forEach(n => {
    const c = cols.get(n) ?? 0
    if (!byCol.has(c)) byCol.set(c, [])
    byCol.get(c)!.push(n)
  })

  const nodes: LayoutNode[] = []
  byCol.forEach((colNames, col) => {
    colNames.forEach((name, row) => {
      nodes.push({
        name,
        col,
        row,
        x: col * (NODE_W + H_GAP),
        y: row * (NODE_H + V_GAP),
      })
    })
  })
  return nodes
}

export default function FlowGraph({ savedFlows, currentFlow, onNavigate }: Props) {
  const graph = useMemo(() => buildCallGraph(savedFlows), [savedFlows])
  const nodes = useMemo(() => layoutGraph(savedFlows, graph), [savedFlows, graph])

  if (nodes.length === 0) {
    return <div style={{ padding: 24, color: 'var(--muted)', fontSize: 13 }}>No flows saved yet.</div>
  }

  const maxX = Math.max(...nodes.map(n => n.x)) + NODE_W
  const maxY = Math.max(...nodes.map(n => n.y)) + NODE_H
  const W = maxX + 40
  const H = maxY + 40

  const nodeMap = new Map(nodes.map(n => [n.name, n]))

  const edges: Array<{ x1: number; y1: number; x2: number; y2: number; key: string }> = []
  graph.forEach((children, parent) => {
    const from = nodeMap.get(parent)
    if (!from) return
    children.forEach(c => {
      const to = nodeMap.get(c)
      if (!to) return
      edges.push({
        x1: from.x + NODE_W,
        y1: from.y + NODE_H / 2,
        x2: to.x,
        y2: to.y + NODE_H / 2,
        key: `${parent}→${c}`,
      })
    })
  })

  return (
    <div style={{ overflowX: 'auto', overflowY: 'auto', maxHeight: 400 }}>
      <svg width={W} height={H} style={{ display: 'block' }}>
        <defs>
          <marker id="arrow" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
            <path d="M0,0 L0,6 L8,3 z" fill="var(--muted)" />
          </marker>
        </defs>
        {edges.map(e => (
          <line
            key={e.key}
            x1={e.x1 + 20} y1={e.y1}
            x2={e.x2 - 4}  y2={e.y2}
            stroke="var(--muted)"
            strokeWidth={1.5}
            markerEnd="url(#arrow)"
          />
        ))}
        {nodes.map(n => {
          const isActive  = n.name === currentFlow
          const isMissing = !savedFlows.find(f => f.name === n.name)
          return (
            <g
              key={n.name}
              transform={`translate(${n.x + 20},${n.y + 20})`}
              style={{ cursor: 'pointer' }}
              onClick={() => onNavigate(n.name)}
            >
              <rect
                x={0} y={0} width={NODE_W} height={NODE_H} rx={6}
                fill={isActive ? 'var(--accent)' : isMissing ? 'rgba(239,68,68,0.15)' : 'rgba(255,255,255,0.06)'}
                stroke={isActive ? 'var(--accent)' : 'rgba(255,255,255,0.15)'}
                strokeWidth={isActive ? 2 : 1}
              />
              <text
                x={NODE_W / 2} y={NODE_H / 2 + 4}
                textAnchor="middle"
                fill={isActive ? '#fff' : 'var(--fg)'}
                fontSize={11}
                fontFamily="inherit"
              >
                {n.name.length > 14 ? n.name.slice(0, 13) + '…' : n.name}
              </text>
            </g>
          )
        })}
      </svg>
    </div>
  )
}
```

### CHANGE 2: Import FlowGraph and add a toggle in FlowDesigner

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx`

**FIND** (exact):
```
import FlowMap from './FlowMap'
```

**REPLACE WITH**:
```
import FlowMap from './FlowMap'
import FlowGraph from './FlowGraph'
```

**FIND** (exact):
```
  const [showFlowMap, setShowFlowMap]   = useState(false)
```

**REPLACE WITH**:
```
  const [showFlowMap, setShowFlowMap]   = useState(false)
  const [showFlowGraph, setShowFlowGraph] = useState(false)
```

Now find the FlowMap render block and add a graph toggle button + FlowGraph below it.

**FIND** (exact):
```
          {showFlowMap && (
            <FlowMap
              flows={savedFlows}
              currentFlow={flowName}
              onNavigate={name => onNavigateToFlow?.(name)}
            />
          )}
```

**REPLACE WITH**:
```
          <div style={{ display: 'flex', gap: 6, marginBottom: 4 }}>
            <button
              className={`btn muted${showFlowMap ? ' active' : ''}`}
              style={{ fontSize: 12 }}
              onClick={() => { setShowFlowMap(p => !p); setShowFlowGraph(false) }}
              title="Flow dependency tree"
            >
              ⬡ Tree
            </button>
            <button
              className={`btn muted${showFlowGraph ? ' active' : ''}`}
              style={{ fontSize: 12 }}
              onClick={() => { setShowFlowGraph(p => !p); setShowFlowMap(false) }}
              title="Flow dependency graph"
            >
              ⬡ Graph
            </button>
          </div>
          {showFlowMap && (
            <FlowMap
              flows={savedFlows}
              currentFlow={flowName}
              onNavigate={name => onNavigateToFlow?.(name)}
            />
          )}
          {showFlowGraph && (
            <FlowGraph
              savedFlows={savedFlows}
              currentFlow={flowName}
              onNavigate={name => onNavigateToFlow?.(name)}
            />
          )}
```

> **NOTE**: If `showFlowMap && (<FlowMap .../>)` is not found with this exact indentation, search for `<FlowMap` in the file — there should be only one occurrence. Add the Tree/Graph toggle buttons before it and the FlowGraph block after it.

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors.

### BROWSER TESTS
1. Save 3 flows where flow A calls B and B calls C.
2. Click "⬡ Graph" button → SVG graph should appear with nodes A, B, C and arrows A→B, B→C.
3. Click node A → should navigate to flow A.
4. Click "⬡ Tree" → tree should appear, graph should hide.
5. Click "⬡ Graph" again → graph appears, tree hides.

### DO NOT TOUCH
- No existing logic in FlowMap.tsx beyond the `export` keyword changes from GRAPH-0.
- The `buildCallGraph` import path must match exactly: `'./FlowMap'`.

---

## APPENDIX: Common Mistakes to Avoid

1. **Do NOT add `console.log` or debug statements** — they were the root cause of a previous Haiku session failure.
2. **Do NOT change the `renderNestedStepCard` toggle logic** — the expand/collapse ▶/▼ is intentional and must remain.
3. **Do NOT rewrite `fieldInput`** — it handles smart condition normalization and source-ref chips; touching it breaks those features.
4. **Do NOT change imports at the top of App.tsx** unless the session explicitly lists it.
5. **Do NOT add error boundaries, try/catch, or fallback UI** — the codebase doesn't use them and they're not requested.
6. **Do NOT create new utility files** unless the session says "Create NEW file".
7. **If a FIND string is not found exactly**, stop and report which string wasn't found — do NOT guess or proceed with approximate matches.
8. **After every session, run `npm run build`** and confirm zero errors before reporting success.
