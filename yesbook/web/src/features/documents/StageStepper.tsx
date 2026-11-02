import type { PipelineStepView, PipelineView } from "./model";
import { formatTimestamp } from "./format";

function ExtractorPaths({ extractors }: { extractors: PipelineStepView["extractors"] }) {
  if (extractors.length === 0) return null;
  return (
    <ul className="extractor-paths" aria-label="Extractor paths">
      {extractors.map((item) => (
        <li
          key={`${item.path}:${item.name}:${item.version}:${String(item.attempt)}`}
          data-path={item.path}
          data-decision={item.decision ?? undefined}
        >
          <span className="extractor-path-label">{item.pathLabel}</span>
          <span className="step-meta">
            {item.name} {item.version}
            {item.decision ? ` · ${item.decision}` : null}
          </span>
          {item.reasonMessage ? (
            <p className="step-message">
              {item.reasonMessage}
              {item.reasonCode ? (
                <>
                  {" "}
                  <code>{item.reasonCode}</code>
                </>
              ) : null}
            </p>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

function StepDetails({ step }: { step: PipelineStepView }) {
  return (
    <>
      {step.workflowId && step.runId ? (
        <p className="step-meta">
          {step.workflowId} · {step.runId}
        </p>
      ) : null}
      {step.retry > 0 ? <p>Retry {String(step.retry)}</p> : null}
      {step.progress && step.progressLabel ? (
        <>
          <progress
            aria-label={`${step.label} progress`}
            max={step.progress.total}
            value={step.progress.current}
          />
          <p>{step.progressLabel}</p>
        </>
      ) : null}
      {(step.id === "route" || step.id === "extract") && step.extractors.length > 0 ? (
        <ExtractorPaths extractors={step.extractors} />
      ) : null}
      {step.message || step.reasonCode ? (
        <p className="step-message">
          {step.message}
          {step.reasonCode ? (
            <>
              {step.message ? " " : null}
              <code>{step.reasonCode}</code>
            </>
          ) : null}
        </p>
      ) : null}
      {step.occurredAt ? <p className="step-meta">{formatTimestamp(step.occurredAt)}</p> : null}
    </>
  );
}

export function StageStepper({ view }: { view: PipelineView }) {
  return (
    <div className="pipeline-view">
      {view.hashMatchMessage ? (
        <p className="pipeline-callout pipeline-callout-hash" role="status">
          {view.hashMatchMessage}
        </p>
      ) : null}
      {view.retryLoop ? (
        <p className="pipeline-callout pipeline-callout-retry" role="status">
          Retry with a different extractor or more context. Attempts, time, and cost are capped
          {view.maxRetry > 0 ? ` (retry ${String(view.maxRetry)})` : ""}.
        </p>
      ) : null}
      <ol className="stepper" aria-label="Processing stages">
        {view.steps.map((step) => (
          <li
            key={step.id}
            data-step={step.id}
            data-state={step.state}
            {...(step.current ? { "aria-current": "step" as const } : {})}
          >
            <div className="step-heading">
              <span className="step-name">{step.label}</span>
              <span className={`step-status state-${step.state}`}>{step.statusLabel}</span>
            </div>
            <StepDetails step={step} />
          </li>
        ))}
      </ol>
      <p className="evidence-note">{view.evidenceNote}</p>
    </div>
  );
}
