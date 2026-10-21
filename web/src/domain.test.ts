import { describe, expect, it, vi } from 'vitest';
import { api, errorBreakdown, metric, pointLabel, routes, validateBenchmark, validateTarget, runCSV } from './domain';
it('rejects duplicate concurrency points before submitting a benchmark', () => {
  expect(validateBenchmark('1,4,4', 30, 256)).toBeTruthy();
  expect(validateBenchmark('1,4,8', 30, 256)).toBeUndefined();
});
describe('honest measurement presentation', () => { it('does not invent unavailable values', () => { for (const value of [undefined, null, NaN, Infinity, '12']) expect(metric(value)).toBe('—'); expect(metric(0, 'ms')).toBe('0 ms'); }); });
describe('navigation and input contracts', () => { it('has all seven workspaces', () => expect(routes).toHaveLength(7)); it('rejects credentials and unsafe endpoint schemes', () => { expect(validateTarget('ftp://localhost', 'target')).toBeTruthy(); expect(validateTarget('http://secret:key@localhost', 'target')).toBeTruthy(); expect(validateTarget('http://localhost:8000', 'target')).toBeUndefined(); }); it('validates workload bounds', () => { expect(validateBenchmark('1,4,8', 30, 256)).toBeUndefined(); for (const c of ['', '0', '-1', '2.4', '257']) expect(validateBenchmark(c, 30, 256)).toBeTruthy(); expect(validateBenchmark('1', 0, 0)).toBeTruthy(); }); });
it('surfaces API errors without rendering success data', async () => { vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 400, json: async () => ({ error: 'Invalid target' }) })); await expect(api('/targets')).rejects.toThrow('Invalid target'); vi.unstubAllGlobals(); });
describe('runCSV', () => {
  it('quotes text cells and leaves unavailable readings empty', () => {
    const csv = runCSV({ id: 'r1', name: 'base, "v2"', targetId: 't', model: 'm', startedAt: '', status: 'complete', findings: [], points: [{ concurrency: 2, requests: 10, errors: 0, rps: 1.5, e2e: { p50: 10, p95: 20, p99: 30 } }] });
    expect(csv.trim().split('\n')[1]).toBe('r1,"base, ""v2""",m,2,10,0,1.5,,,,,,,10,20,30,,,,,');
  });
});
describe('open-loop workloads', () => {
  it('requires one in-flight cap and positive unique rates', () => {
    expect(validateBenchmark('64', 30, 256, '1,2.5,8')).toBeUndefined();
    for (const [cap, rates] of [['1,2', '1'], ['0', '1'], ['64', ''], ['64', '0'], ['64', '2,2'], ['64', '1,,2']]) expect(validateBenchmark(cap, 30, 256, rates)).toBeTruthy();
  });
  it('labels points by rate and orders error classes by count', () => {
    expect(pointLabel({ concurrency: 64, requestRate: 2.5, requests: 1, errors: 0, rps: 1 })).toBe('2.5 req/s');
    expect(pointLabel({ concurrency: 4, requests: 1, errors: 0, rps: 1 })).toBe('concurrency 4');
    expect(errorBreakdown({ concurrency: 1, requests: 4, errors: 4, rps: 0, errorKinds: { timeout: 1, http_5xx: 3 } })).toBe('http_5xx 3 · timeout 1');
  });
});
