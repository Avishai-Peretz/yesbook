import type { PathDecisionView } from "./model";
import { formatTimestamp } from "./format";

export function PathDecisions({ decisions }: { decisions: readonly PathDecisionView[] }) {
  if (decisions.length === 0) {
    return <p>No path decisions recorded yet.</p>;
  }

  return (
    <ol className="path-decisions" aria-label="Extraction path decisions">
      {decisions.map((decision, index) => (
        <li
          key={`${decision.kind}-${decision.reasonCode}-${decision.extractor ?? "none"}-${String(index)}`}
          data-kind={decision.kind}
        >
          <div className="path-decision-heading">
            <span className={`path-decision-kind kind-${decision.kind}`}>{decision.kindLabel}</span>
            {decision.pathLabel || decision.extractor ? (
              <span className="path-decision-extractor">
                {decision.pathLabel ?? decision.extractor}
                {decision.extractor && decision.pathLabel !== decision.extractor
                  ? ` (${decision.extractor})`
                  : null}
              </span>
            ) : null}
          </div>
          <p className="path-decision-message">{decision.reasonMessage}</p>
          <p className="step-meta">
            <code>{decision.reasonCode}</code>
            {decision.targetId ? <> · {decision.targetId}</> : null}
            {decision.occurredAt ? <> · {formatTimestamp(decision.occurredAt)}</> : null}
          </p>
        </li>
      ))}
    </ol>
  );
}
