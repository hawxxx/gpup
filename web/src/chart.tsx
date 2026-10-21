import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';
export function Chart({ x, y, label, color = '#91f2a9', time = true }: { x: number[]; y: (number | null)[]; label: string; color?: string; time?: boolean }) {
  const host = useRef<HTMLDivElement>(null); const plotRef = useRef<uPlot | null>(null); const hasData = x.length > 0;
  // Snapshots arrive every second; build the plot once per style and stream data into it.
  useEffect(() => {
    if (!host.current || !hasData) return;
    const plot = new uPlot({ width: Math.max(200, host.current.clientWidth), height: 190, padding: [12, 12, 0, 0], cursor: { drag: { x: false, y: false } }, legend: { show: false }, scales: { x: { time } }, axes: [{ stroke: '#788594', grid: { stroke: '#242d36', width: 1 }, ticks: { show: false }, font: '11px monospace' }, { stroke: '#788594', grid: { stroke: '#242d36', width: 1 }, ticks: { show: false }, font: '11px monospace', size: 52 }], series: [{}, { label, stroke: color, width: 2, fill: `${color}10`, spanGaps: false, points: { show: false } }] }, [x.slice(-600), y.slice(-600)], host.current); plotRef.current = plot;
    const observer = new ResizeObserver(entries => { const width = entries[0]?.contentRect.width; if (width) plot.setSize({ width: Math.max(200, width), height: 190 }); });
    observer.observe(host.current);
    return () => { observer.disconnect(); plot.destroy(); plotRef.current = null; };
  }, [hasData, label, color, time]);
  useEffect(() => { if (x.length) plotRef.current?.setData([x.slice(-600), y.slice(-600)]); }, [x, y]);
  return x.length ? <div ref={host} role="img" aria-label={`${label} chart with ${x.length} observations`} className="chart" /> : <div className="chart-empty">Waiting for measured history</div>;
}
