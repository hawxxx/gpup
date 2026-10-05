// @vitest-environment jsdom
import React from 'react';
import { render, screen, cleanup } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { ComparisonView } from './comparison';
afterEach(cleanup);
it('shows percentage changes from measured comparison and preserves unknown values', () => { render(<ComparisonView result={{ compatible: true, baselineId: 'a', candidateId: 'b', changes: [{ concurrency: 8, rpsPercent: 12, latencyP95Percent: -4, throughputMetric: 'rps' }] }} />); expect(screen.getByText('+12 %').className).toBe('delta-good'); expect(screen.getByText('-4 %').className).toBe('delta-good'); expect(screen.getAllByText('—')).toHaveLength(2); });
