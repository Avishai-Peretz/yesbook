import type { ContentResponse, ExtractedContent, ReviewItem } from "../../api/contracts";
import { formatCoverage } from "../documents/format";
import {
  extractorPathFor,
  extractorPathLabel,
  type PathDecisionView,
} from "../documents/model";
import { ContentBlockView } from "./ContentBlock";
import { contentOutcomeLabel, reviewItemLabel } from "./labels";
import { ReviewPanel } from "./ReviewPanel";

export type ContentViewerState =
  | { status: "unavailable"; message: string }
  | { status: "loading" }
  | { status: "error"; message: string; onRetry: () => void }
  | { status: "empty"; content: ExtractedContent }
  | { status: "ready"; response: ContentResponse };

interface ContentViewerProps {
  state: ContentViewerState;
  fileName: string;
  pathDecisions?: readonly PathDecisionView[];
}

function WarningList({
  title,
  items,
  tone,
}: {
  title: string;
  items: readonly ReviewItem[];
  tone: "review" | "missing";
}) {
  if (items.length === 0) return null;
  return (
    <section className={`content-warnings content-warnings-${tone}`} aria-labelledby={`${tone}-heading`}>
      <h3 id={`${tone}-heading`}>{title}</h3>
      <ul>
        {items.map((item) => (
          <li key={`${item.target_id}-${item.reason}`}>{reviewItemLabel(item)}</li>
        ))}
      </ul>
    </section>
  );
}

function acceptedExtractorPaths(content: ExtractedContent): string {
  const paths = new Map<string, string>();
  for (const block of content.blocks) {
    const path = extractorPathFor(block.provenance.extractor);
    const key = `${path}:${block.provenance.extractor}`;
    if (!paths.has(key)) {
      paths.set(
        key,
        `${extractorPathLabel(path)} (${block.provenance.extractor} ${block.provenance.extractor_version})`,
      );
    }
  }
  return [...paths.values()].join(" · ");
}

function ContentMeta({ content }: { content: ExtractedContent }) {
  const extractors = acceptedExtractorPaths(content);
  return (
    <dl className="meta-list">
      <div>
        <dt>Outcome</dt>
        <dd data-status={content.status}>{contentOutcomeLabel(content.status)}</dd>
      </div>
      <div>
        <dt>Coverage</dt>
        <dd>{formatCoverage(content.coverage)}</dd>
      </div>
      <div>
        <dt>Pages</dt>
        <dd>{String(content.pages)}</dd>
      </div>
      <div>
        <dt>Pipeline</dt>
        <dd>{content.pipeline_version}</dd>
      </div>
      {extractors ? (
        <div>
          <dt>Extractors</dt>
          <dd>{extractors}</dd>
        </div>
      ) : null}
    </dl>
  );
}

export function ContentViewer({
  state,
  fileName,
  pathDecisions = [],
}: ContentViewerProps) {
  if (state.status === "unavailable") {
    return (
      <section className="content-results" aria-labelledby="extracted-content-heading">
        <h2 id="extracted-content-heading">Extracted content</h2>
        <p>{state.message}</p>
      </section>
    );
  }

  if (state.status === "loading") {
    return (
      <section
        className="content-results"
        aria-labelledby="extracted-content-heading"
        aria-busy="true"
      >
        <h2 id="extracted-content-heading">Extracted content</h2>
        <p role="status">Loading extracted content…</p>
      </section>
    );
  }

  if (state.status === "error") {
    return (
      <section className="content-results" aria-labelledby="extracted-content-heading">
        <h2 id="extracted-content-heading">Extracted content</h2>
        <div className="empty-state" role="alert">
          <h3>Content unavailable</h3>
          <p>{state.message}</p>
          <button type="button" onClick={state.onRetry}>
            Retry
          </button>
        </div>
      </section>
    );
  }

  if (state.status === "empty") {
    const emptyReview =
      state.content.status === "needs_review" ? (
        <ReviewPanel content={state.content} decisions={pathDecisions} />
      ) : null;
    return (
      <section className="content-results" aria-labelledby="extracted-content-heading">
        <h2 id="extracted-content-heading">Extracted content</h2>
        <ContentMeta content={state.content} />
        {emptyReview}
        <div className="empty-state" role="status">
          <h3>No blocks extracted</h3>
          <p>This document published successfully, but no content blocks were returned.</p>
        </div>
      </section>
    );
  }

  const { content } = state.response;
  const review = content.review ?? [];
  const missing = content.missing ?? [];

  return (
    <section className="content-results" aria-labelledby="extracted-content-heading">
      <h2 id="extracted-content-heading">Extracted content</h2>
      <p className="visually-hidden" role="status">
        {fileName} content is {contentOutcomeLabel(content.status).toLowerCase()} with{" "}
        {formatCoverage(content.coverage)} coverage and {String(content.blocks.length)} blocks.
      </p>
      <ContentMeta content={content} />
      {content.status === "needs_review" ? (
        <ReviewPanel content={content} decisions={pathDecisions} />
      ) : null}
      {content.status === "partial" ? (
        <p className="content-banner content-banner-partial" role="status">
          Extraction finished with partial coverage. Some pages are missing.
        </p>
      ) : null}
      {content.status !== "needs_review" ? (
        <WarningList title="Review warnings" items={review} tone="review" />
      ) : null}
      {content.status !== "needs_review" ? (
        <WarningList title="Missing pages" items={missing} tone="missing" />
      ) : null}
      <ol className="content-block-list">
        {content.blocks.map((block, index) => (
          <li key={`${block.kind}-${String(block.page)}-${String(index)}`}>
            <ContentBlockView block={block} index={index} />
          </li>
        ))}
      </ol>
    </section>
  );
}
