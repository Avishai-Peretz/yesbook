import type { ExtractedContent, ReviewItem } from "../../api/contracts";
import type { PathDecisionView } from "../documents/model";
import { reviewItemLabel } from "./labels";
import { buildReviewGuidance } from "./reviewGuidance";

interface ReviewPanelProps {
  content: ExtractedContent;
  decisions?: readonly PathDecisionView[];
}

function CodeList({ label, codes }: { label: string; codes: readonly string[] }) {
  if (codes.length === 0) return null;
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        <ul className="review-code-list">
          {codes.map((code) => (
            <li key={code}>
              <code>{code}</code>
            </li>
          ))}
        </ul>
      </dd>
    </div>
  );
}

function WarningList({ title, items }: { title: string; items: readonly ReviewItem[] }) {
  if (items.length === 0) return null;
  return (
    <div className="review-panel-section">
      <h3>{title}</h3>
      <ul>
        {items.map((item) => (
          <li key={`${item.target_id}-${item.reason}`}>
            {reviewItemLabel(item)}
            {item.code ? (
              <>
                {" "}
                <code>{item.code}</code>
              </>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ReviewPanel({ content, decisions = [] }: ReviewPanelProps) {
  const review = content.review ?? [];
  const missing = content.missing ?? [];
  const guidance = buildReviewGuidance(content, decisions);

  return (
    <section className="review-panel" aria-labelledby="review-panel-heading">
      <h2 id="review-panel-heading">Needs review</h2>
      <p className="lede compact" role="status">
        This document needs review before downstream use. Use the checks below to decide the next
        action.
      </p>
      <dl className="meta-list review-panel-meta">
        <div>
          <dt>Accepted coverage</dt>
          <dd>{guidance.coverageLabel}</dd>
        </div>
        <div>
          <dt>Blocks available</dt>
          <dd>{String(content.blocks.length)}</dd>
        </div>
        <CodeList label="Failed codes" codes={guidance.failedCodes} />
        <CodeList label="Failed checks" codes={guidance.failedChecks} />
        {guidance.missingFigures ? (
          <div>
            <dt>Figures</dt>
            <dd>Embedded images were not represented as figure blocks.</dd>
          </div>
        ) : null}
      </dl>
      <WarningList title="Review warnings" items={review} />
      <WarningList title="Missing pages" items={missing} />
      <div className="review-panel-section review-next-action">
        <h3>Recommended next action</h3>
        <p>{guidance.recommendedAction}</p>
      </div>
    </section>
  );
}
