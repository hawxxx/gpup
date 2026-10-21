// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ActionDialog } from './dialogs';
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('opens an accessible target dialog and rejects endpoint credentials before fetching', async () => {
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
  render(<ActionDialog kind="target" refresh={() => {}} disabled={false} />);
  fireEvent.click(screen.getByRole('button', { name: '+ Add target' }));
  expect(screen.getByRole('dialog', { name: 'Connect an inference target' })).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Local' } });
  fireEvent.change(screen.getByLabelText('Endpoint URL'), { target: { value: 'http://secret:key@localhost' } });
  fireEvent.click(screen.getByRole('button', { name: 'Connect target' }));
  expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'Use an HTTP(S) endpoint without embedded credentials.');
  expect(fetch).not.toHaveBeenCalled();
});
it('keeps target dialog open and presents server rejection', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, json: async () => ({ error: 'Read-only server' }) }));
  const refresh = vi.fn(); render(<ActionDialog kind="target" refresh={refresh} disabled={false} />);
  fireEvent.click(screen.getByRole('button', { name: '+ Add target' }));
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Local' } });
  fireEvent.change(screen.getByLabelText('Endpoint URL'), { target: { value: 'http://localhost:8000' } });
  fireEvent.click(screen.getByRole('button', { name: 'Connect target' }));
  await waitFor(() => expect(screen.getByRole('alert').textContent).toBe('Read-only server'));
  expect(refresh).not.toHaveBeenCalled(); expect(screen.getByRole('dialog')).toBeTruthy();
});
it('submits an open-loop sweep with SLO targets', async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, status: 202, json: async () => ({ id: 'run-1' }) }); vi.stubGlobal('fetch', fetch);
  const snapshot = { targets: [{ id: 't1', name: 'local', engine: 'vllm', url: 'http://localhost:8000', model: 'm' }], models: [] } as unknown as import('./domain').Snapshot;
  render(<ActionDialog kind="benchmark" snapshot={snapshot} refresh={() => {}} disabled={false} />);
  fireEvent.click(screen.getByRole('button', { name: '↗ Run benchmark' }));
  fireEvent.change(screen.getByLabelText('Target'), { target: { value: 't1' } });
  fireEvent.change(screen.getByLabelText('Prompt'), { target: { value: 'hello' } });
  fireEvent.click(screen.getByLabelText('Request-rate sweep'));
  expect((screen.getByLabelText('In-flight cap') as HTMLInputElement).value).toBe('64');
  fireEvent.change(screen.getByLabelText('Request rates (req/s)'), { target: { value: '0.5, 2' } });
  fireEvent.change(screen.getByLabelText('TTFT ≤ (ms)'), { target: { value: '300' } });
  fireEvent.click(screen.getByRole('button', { name: 'Start benchmark' }));
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  const body = JSON.parse(fetch.mock.calls[0][1].body);
  expect(body).toMatchObject({ targetId: 't1', model: 'm', concurrency: [64], requestRates: [0.5, 2], slo: { ttftMs: 300 } });
  expect(body.slo.tpotMs).toBeUndefined();
});
it('rejects an open-loop sweep with several in-flight caps before fetching', async () => {
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
  const snapshot = { targets: [{ id: 't1', name: 'local', engine: 'vllm', url: 'http://localhost:8000', model: 'm' }], models: [] } as unknown as import('./domain').Snapshot;
  render(<ActionDialog kind="benchmark" snapshot={snapshot} refresh={() => {}} disabled={false} />);
  fireEvent.click(screen.getByRole('button', { name: '↗ Run benchmark' }));
  fireEvent.change(screen.getByLabelText('Target'), { target: { value: 't1' } });
  fireEvent.change(screen.getByLabelText('Prompt'), { target: { value: 'hello' } });
  fireEvent.click(screen.getByLabelText('Request-rate sweep'));
  fireEvent.change(screen.getByLabelText('In-flight cap'), { target: { value: '4,8' } });
  fireEvent.click(screen.getByRole('button', { name: 'Start benchmark' }));
  expect((await screen.findByRole('alert')).textContent).toContain('In-flight cap');
  expect(fetch).not.toHaveBeenCalled();
});
