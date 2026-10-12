import React, { useEffect, useState } from 'react';
import { AppVersion, RoutingRule } from '../types';

interface RoutingTabProps {
  logicalApp: string;
  versions: AppVersion[];
  connectionMode: 'db' | 'direct';
}

export const RoutingTab: React.FC<RoutingTabProps> = ({ logicalApp, versions, connectionMode }) => {
  const [rules, setRules] = useState<RoutingRule[]>([]);
  const [loading, setLoading] = useState(false);
  const [pushPending, setPushPending] = useState(false);

  const activeVersions = versions.filter(v => v.status === 'active');

  useEffect(() => {
    setLoading(true);
    fetch(`/api/apps/${logicalApp}/routing`)
      .then(r => r.json())
      .then(data => setRules(data || []))
      .finally(() => setLoading(false));
  }, [logicalApp]);

  const updateRule = async (tenantId: number | null, activeApp: string, env = 'production') => {
    await fetch(`/api/apps/${logicalApp}/routing`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ rules: [{ tenant_id: tenantId, active_app: activeApp, environment: env }] }),
    });
    if (connectionMode === 'direct') setPushPending(true);
    const updated = await fetch(`/api/apps/${logicalApp}/routing`).then(r => r.json());
    setRules(updated || []);
  };

  const promote = async (appName: string, env = 'production') => {
    const ver = appName.split('-').pop();
    await fetch(`/api/apps/${logicalApp}/versions/${ver}/promote?environment=${env}`, { method: 'POST' });
    const updated = await fetch(`/api/apps/${logicalApp}/routing`).then(r => r.json());
    setRules(updated || []);
  };

  const defaultRule = rules.find(r => r.tenant_id === null);
  const tenantRules = rules.filter(r => r.tenant_id !== null);

  return (
    <div style={{ padding: '16px' }}>
      {/* Connection mode indicator */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16 }}>
        <span style={{
          display: 'inline-flex', alignItems: 'center', gap: 6,
          padding: '4px 10px', borderRadius: 12,
          background: connectionMode === 'db' ? '#e6f7e6' : '#fff8e1',
          color: connectionMode === 'db' ? '#2e7d32' : '#f57f17',
          fontSize: 12, fontWeight: 500,
        }}>
          <span style={{ fontSize: 8 }}>{connectionMode === 'db' ? '●' : '●'}</span>
          {connectionMode === 'db' ? 'Live Sync (shared DB)' : 'Manual Push'}
        </span>
        {connectionMode === 'direct' && pushPending && (
          <button
            onClick={() => setPushPending(false)}
            style={{ fontSize: 12, padding: '4px 10px', cursor: 'pointer' }}
          >
            Push to Gateway
          </button>
        )}
      </div>

      {loading ? <p>Loading…</p> : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid #e0e0e0' }}>
              <th style={{ textAlign: 'left', padding: '6px 8px' }}>Tenant</th>
              <th style={{ textAlign: 'left', padding: '6px 8px' }}>Environment</th>
              <th style={{ textAlign: 'left', padding: '6px 8px' }}>Active Version</th>
              <th style={{ padding: '6px 8px' }}></th>
            </tr>
          </thead>
          <tbody>
            {/* Default row always first */}
            <tr style={{ background: '#fafafa', borderBottom: '1px solid #f0f0f0' }}>
              <td style={{ padding: '6px 8px', color: '#666' }}>Default</td>
              <td style={{ padding: '6px 8px' }}>production</td>
              <td style={{ padding: '6px 8px' }}>
                <select
                  value={defaultRule?.active_app ?? ''}
                  onChange={e => updateRule(null, e.target.value)}
                  style={{ fontSize: 12, padding: '2px 4px' }}
                >
                  <option value="">— unset —</option>
                  {activeVersions.map(v => (
                    <option key={v.app_name} value={v.app_name}>{v.version}</option>
                  ))}
                </select>
              </td>
              <td style={{ padding: '6px 8px' }}></td>
            </tr>

            {tenantRules.map(rule => (
              <tr key={`${rule.tenant_id}-${rule.environment}`} style={{ borderBottom: '1px solid #f0f0f0' }}>
                <td style={{ padding: '6px 8px' }}>Tenant {rule.tenant_id}</td>
                <td style={{ padding: '6px 8px' }}>{rule.environment}</td>
                <td style={{ padding: '6px 8px' }}>
                  <select
                    value={rule.active_app}
                    onChange={e => updateRule(rule.tenant_id, e.target.value, rule.environment)}
                    style={{ fontSize: 12, padding: '2px 4px' }}
                  >
                    {activeVersions.map(v => (
                      <option key={v.app_name} value={v.app_name}>{v.version}</option>
                    ))}
                  </select>
                </td>
                <td style={{ padding: '6px 8px' }}>
                  <button
                    onClick={() => promote(rule.active_app, rule.environment)}
                    style={{ fontSize: 11, padding: '2px 8px', cursor: 'pointer' }}
                    title="Set as default for all tenants"
                  >
                    Promote to default
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
};
