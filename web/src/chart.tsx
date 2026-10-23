import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';
type Tone = 'green' | 'purple' | 'blue';
function themeColors(tone: Tone) { const style = getComputedStyle(document.documentElement); return { axis: style.getPropertyValue('--muted').trim() || '#788594', grid: style.getPropertyValue('--border').trim() || '#242d36', line: style.getPropertyValue(`--chart-${tone}`).trim() || '#91f2a9' }; }
/** Time charts stream history; sweep charts (time=false) mark each measured point and label only measured x values. */
export function Chart({ x, y, label, tone = 'green', time = true }: { x: number[]; y: (number | null)[]; label: string; tone?: Tone; time?: boolean }) {
  const host = useRef<HTMLDivElement>(null); const plotRef = useRef<uPlot | null>(null); const hasData = x.length > 0;
  // Snapshots arrive every second; build the plot once per style and stream data into it.
  useEffect(() => {
    if (!host.current || !hasData) return;
    const theme = themeColors(tone);
    const plot = new uPlot({ width: Math.max(200, host.current.clientWidth), height: 190, padding: [12, 12, 0, 0], cursor: { drag: { x: false, y: false } }, legend: { show: false }, scales: { x: { time }, y: { range: (_u, _min, max) => [0, max > 0 ? max * 1.1 : 1] } }, axes: [{ stroke: theme.axis, grid: { stroke: theme.grid, width: 1 }, ticks: { show: false }, font: '11px monospace', ...(time ? {} : { splits: (u: uPlot) => (u.data[0] as number[]).slice(), values: (_u: uPlot, splits: number[]) => splits.map(v => v.toLocaleString()) }) }, { stroke: theme.axis, grid: { stroke: theme.grid, width: 1 }, ticks: { show: false }, font: '11px monospace', size: 52 }], series: [{}, { label, stroke: theme.line, width: 2, fill: `${theme.line}14`, spanGaps: false, points: { show: !time, size: 7, fill: theme.line } }] }, [x.slice(-600), y.slice(-600)], host.current); plotRef.current = plot;
    const observer = new ResizeObserver(entries => { const width = entries[0]?.contentRect.width; if (width) plot.setSize({ width: Math.max(200, width), height: 190 }); });
    observer.observe(host.current);
    return () => { observer.disconnect(); plot.destroy(); plotRef.current = null; };
  }, [hasData, label, tone, time, document.documentElement.dataset.theme]);
  useEffect(() => { if (x.length) plotRef.current?.setData([x.slice(-600), y.slice(-600)]); }, [x, y]);
  return x.length ? <div ref={host} role="img" aria-label={`${label} chart with ${x.length} observations`} className="chart" /> : <div className="chart-empty">Waiting for measured history</div>;
}
