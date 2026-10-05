import { Link } from '@tanstack/react-router';
import { ArrowRight, ArrowUpRight, Check, ScanLine } from 'lucide-react';
import type { Snapshot } from './domain';

export function OverviewGuide({ snapshot }: { snapshot: Snapshot }) {
  const completed = snapshot.runs.filter(run => run.status === 'complete').length;
  return <section className="overview-guide" aria-label="Performance workflow">
    <div className="workflow-heading"><span className="workflow-icon"><ScanLine size={18} /></span><div><h2>Turn measurements into decisions</h2><p>Connect an engine, find its operating point, then compare the next change.</p></div><span className="badge">{completed} completed runs</span></div>
    <ol className="workflow-steps">
      <li><Link to="/system"><span className="step-number">{snapshot.targets.length ? <Check size={13} /> : '01'}</span><span>Connect<span className="step-caption">Configure an endpoint</span></span><ArrowRight size={14} /></Link></li>
      <li><Link to="/benchmarks"><span className="step-number">02</span><span>Benchmark<span className="step-caption">Find the concurrency knee</span></span><ArrowRight size={14} /></Link></li>
      <li><Link to="/gpus"><span className="step-number">03</span><span>Observe<span className="step-caption">Inspect local hardware</span></span><ArrowRight size={14} /></Link></li>
      <li><Link to="/experiments"><span className="step-number">04</span><span>Compare<span className="step-caption">Accept or reject a change</span></span><ArrowUpRight size={14} /></Link></li>
    </ol>
    <span className="workflow-border" aria-hidden="true" />
    {snapshot.findings.length > 0 && <div className="overview-findings"><div className="findings-heading"><h2>Performance findings</h2><span>Measured evidence · workspace scope</span></div>{snapshot.findings.map((finding, index) => <article key={`${finding.kind}-${index}`} className="overview-finding"><div><span className={`badge ${finding.severity === 'warning' || finding.severity === 'critical' ? 'warning' : ''}`}>{finding.kind} · {finding.confidence} confidence</span><h3>{finding.summary}</h3></div><ul>{finding.evidence.map(evidence => <li key={evidence}>{evidence}</li>)}</ul><p>{finding.recommendation}</p></article>)}</div>}
  </section>;
}
