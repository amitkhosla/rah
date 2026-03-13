import { useState } from 'react'

interface Props {
  accent: string
  setAccent: (c: string) => void
}

export default function Settings({ accent, setAccent }: Props) {
  const [density, setDensity] = useState<'comfortable' | 'compact'>('comfortable')
  const [fontScale, setFontScale] = useState('1')

  function apply() {
    document.body.style.fontSize = `${16 * parseFloat(fontScale)}px`
    document.querySelectorAll<HTMLElement>('.panel-body').forEach(el => {
      el.style.padding = density === 'compact' ? '8px' : '12px'
    })
  }

  return (
    <div className="two-col">
      <div className="panel">
        <div className="panel-header">UI Customization</div>
        <div className="panel-body">
          <label className="hint">Accent colour</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 6 }}>
            <input
              type="color"
              className="color-input"
              value={accent}
              onChange={e => setAccent(e.target.value)}
            />
            <span className="hint">{accent}</span>
          </div>

          <label className="hint">Density</label>
          <select
            className="input mt4"
            value={density}
            onChange={e => setDensity(e.target.value as 'comfortable' | 'compact')}
          >
            <option value="comfortable">Comfortable</option>
            <option value="compact">Compact</option>
          </select>

          <label className="hint">Font Scale</label>
          <select
            className="input mt4"
            value={fontScale}
            onChange={e => setFontScale(e.target.value)}
          >
            <option value="0.92">Small</option>
            <option value="1">Default</option>
            <option value="1.08">Large</option>
          </select>

          <button className="btn mt12" onClick={apply}>Apply</button>
        </div>
      </div>

      <div className="panel">
        <div className="panel-header">Notes</div>
        <div className="panel-body">
          <p className="hint">
            Accent colour changes apply immediately.{'\n'}
            Density and font scale require clicking Apply.{'\n\n'}
            Flow Designer: drag blocks from the palette onto the canvas.{'\n'}
            API Definition: add APIs manually or import from an OpenAPI spec.{'\n'}
            Deploy: select targets by level or name, create a release and deploy.
          </p>
        </div>
      </div>
    </div>
  )
}
