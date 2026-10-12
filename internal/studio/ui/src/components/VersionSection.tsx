import React, { useEffect, useState } from 'react';
import { AppVersion, CreateVersionRequest } from '../types';
import { RoutingTab } from './RoutingTab';

interface VersionSectionProps {
  logicalApp: string;
  connectionMode?: 'db' | 'direct';
}

export const VersionSection: React.FC<VersionSectionProps> = ({ logicalApp, connectionMode = 'db' }) => {
  const [versions, setVersions] = useState<AppVersion[]>([]);
  const [expanded, setExpanded] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [showRouting, setShowRouting] = useState(false);
  const [form, setForm] = useState<CreateVersionRequest>({ source_version: '', target_version: '', changelog: '' });
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');

  const loadVersions = () => {
    fetch(`/api/apps/${logicalApp}/versions`)
      .then(r => r.json())
      .then(data => setVersions(data || []));
  };

  useEffect(() => { if (expanded) loadVersions(); }, [expanded, logicalApp]);

  const statusColor = (status: AppVersion['status']) => ({
    draft: '#9e9e9e',
    active: '#2e7d32',
    retired: '#bdbdbd',
  }[status]);

  const createVersion = async () => {
    setCreating(true); setError('');
    const res = await fetch(`/api/apps/${logicalApp}/versions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(form),
    });
    setCreating(false);
    if (!res.ok) {
      const text = await res.text();
      setError(text);
      return;
    }
    setShowCreate(false);
    setForm({ source_version: '', target_version: '', changelog: '' });
    loadVersions();
  };

  return (
    <div style={{ marginTop: 8, borderTop: '1px solid #f0f0f0' }}>
      <button
        onClick={() => setExpanded(!expanded)}
        style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 12, color: '#1976d2', padding: '6px 0' }}
      >
        {expanded ? '▼' : '▶'} Versions {versions.length > 0 && `(${versions.length})`}
      </button>

      {expanded && (
        <div style={{ paddingLeft: 12 }}>
          {/* Version list */}
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 8 }}>
            {versions.map(v => (
              <span key={v.app_name} style={{
                display: 'inline-flex', alignItems: 'center', gap: 6,
                padding: '3px 10px', borderRadius: 12, fontSize: 12,
                background: '#f5f5f5', border: '1px solid #e0e0e0',
              }}>
                <span style={{ fontWeight: 600 }}>{v.version}</span>
                <span style={{ color: statusColor(v.status), fontSize: 11, textTransform: 'uppercase' }}>
                  {v.status}
                </span>
              </span>
            ))}
            <button
              onClick={() => setShowCreate(true)}
              style={{ fontSize: 12, padding: '3px 10px', cursor: 'pointer', borderRadius: 12 }}
            >
              + New version
            </button>
          </div>

          {/* Routing toggle */}
          <button
            onClick={() => setShowRouting(!showRouting)}
            style={{ fontSize: 12, background: 'none', border: 'none', cursor: 'pointer', color: '#555', marginBottom: 8 }}
          >
            {showRouting ? '▼' : '▶'} Routing
          </button>
          {showRouting && <RoutingTab logicalApp={logicalApp} versions={versions} connectionMode={connectionMode} />}

          {/* Create version modal */}
          {showCreate && (
            <div style={{
              position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)',
              display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
            }}>
              <div style={{ background: '#fff', borderRadius: 8, padding: 24, width: 400, boxShadow: '0 4px 20px rgba(0,0,0,0.15)' }}>
                <h3 style={{ margin: '0 0 16px' }}>New version of {logicalApp}</h3>

                <label style={{ display: 'block', marginBottom: 12 }}>
                  <span style={{ fontSize: 12, color: '#666' }}>Source version</span>
                  <select
                    value={form.source_version}
                    onChange={e => setForm(f => ({ ...f, source_version: e.target.value }))}
                    style={{ display: 'block', width: '100%', marginTop: 4, padding: '6px 8px' }}
                  >
                    <option value="">Select…</option>
                    {versions.map(v => <option key={v.version} value={v.version}>{v.version} ({v.status})</option>)}
                  </select>
                </label>

                <label style={{ display: 'block', marginBottom: 12 }}>
                  <span style={{ fontSize: 12, color: '#666' }}>New version name</span>
                  <input
                    value={form.target_version}
                    onChange={e => setForm(f => ({ ...f, target_version: e.target.value }))}
                    placeholder="e.g. v2"
                    style={{ display: 'block', width: '100%', marginTop: 4, padding: '6px 8px', boxSizing: 'border-box' }}
                  />
                </label>

                <label style={{ display: 'block', marginBottom: 16 }}>
                  <span style={{ fontSize: 12, color: '#666' }}>Changelog</span>
                  <textarea
                    value={form.changelog}
                    onChange={e => setForm(f => ({ ...f, changelog: e.target.value }))}
                    rows={3}
                    style={{ display: 'block', width: '100%', marginTop: 4, padding: '6px 8px', boxSizing: 'border-box' }}
                  />
                </label>

                {error && <p style={{ color: '#c62828', fontSize: 12, margin: '0 0 12px' }}>{error}</p>}

                <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                  <button onClick={() => { setShowCreate(false); setError(''); }}>Cancel</button>
                  <button
                    onClick={createVersion}
                    disabled={creating || !form.source_version || !form.target_version}
                    style={{ background: '#1976d2', color: '#fff', border: 'none', padding: '6px 16px', borderRadius: 4, cursor: 'pointer' }}
                  >
                    {creating ? 'Creating…' : `Create ${form.target_version || ''} (Draft)`}
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
};
