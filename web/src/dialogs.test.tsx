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
