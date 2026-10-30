import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { isFinalDocumentStatus } from "../../api/contracts";
import { useAppSources } from "../../app/sources";
import { describeFailure } from "../../app/messages";
import { ExtractedContentSection } from "../content/ExtractedContentSection";
import { DeleteDocumentControl } from "./DeleteDocumentControl";
import { formatBytes, formatCostMicros } from "./format";
import { PathDecisions } from "./PathDecisions";
import { startTimelinePoll } from "./polling";
import { StageStepper } from "./StageStepper";
import {
  attemptStatusLabel,
  collectPathDecisions,
  derivePipelineView,
  extractionModeLabel,
  extractorPathFor,
  extractorPathLabel,
  pathDecisionKindLabel,
  statusLabel,
  timelineSummary,
} from "./model";

export function DocumentDetailPage() {
  const { documentId } = useParams();
  const sources = useAppSources();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const stopPollingRef = useRef<(() => void) | null>(null);
  const id = documentId ?? "";

  const timelineQuery = useQuery({
    queryKey: ["timeline", id],
    enabled: id.length > 0,
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: ({ signal }) => sources.documents.getTimeline(id, signal),
  });

  const deleteMutation = useMutation({
    mutationFn: () =>
      sources.documents.deleteDocument(id, new AbortController().signal),
    onSuccess: async () => {
      stopPollingRef.current?.();
      stopPollingRef.current = null;
      queryClient.removeQueries({ queryKey: ["timeline", id] });
      queryClient.removeQueries({ queryKey: ["content", id] });
      await queryClient.invalidateQueries({ queryKey: ["documents"] });
      void navigate("/documents");
    },
  });

  const status = timelineQuery.data?.document.status;

  useEffect(() => {
    if (!timelineQuery.isSuccess || !status || isFinalDocumentStatus(status)) {
      stopPollingRef.current = null;
      return;
    }
    const controller = new AbortController();
    const stop = startTimelinePoll({
      signal: controller.signal,
      intervalMs: sources.timelinePollIntervalMs,
      maxIntervalMs: sources.timelinePollMaxIntervalMs,
      fetchTimeline: (signal) => sources.documents.getTimeline(id, signal),
      isTerminal: (timeline) => isFinalDocumentStatus(timeline.document.status),
      onTimeline: (timeline) => {
        setRefreshError(null);
        queryClient.setQueryData(["timeline", id], timeline);
      },
      onError: (error) => {
        setRefreshError(describeFailure(error, "The latest update failed. Retrying."));
      },
    });
    stopPollingRef.current = () => {
      stop();
      controller.abort();
    };
    return () => {
      stop();
      controller.abort();
      stopPollingRef.current = null;
    };
  }, [
    id,
    queryClient,
    sources,
    status,
    timelineQuery.isSuccess,
  ]);

  if (!documentId) {
    return (
      <section aria-labelledby="document-heading">
        <h1 id="document-heading">Document</h1>
        <p role="alert">That document was not found.</p>
      </section>
    );
  }

  if (timelineQuery.isPending) {
    return (
      <section aria-labelledby="document-heading" aria-busy="true">
        <p className="eyebrow">Document</p>
        <h1 id="document-heading">Document</h1>
        <p role="status">Loading document timeline…</p>
      </section>
    );
  }

  if (timelineQuery.isError) {
    return (
      <section aria-labelledby="document-heading">
        <p className="eyebrow">Document</p>
        <h1 id="document-heading">Document</h1>
        <div className="empty-state" role="alert">
          <h2>Timeline unavailable</h2>
          <p>
            {describeFailure(
              timelineQuery.error,
              "The document timeline could not be loaded. Retry.",
            )}
          </p>
          <button
            type="button"
            onClick={() => {
              void timelineQuery.refetch();
            }}
          >
            Retry
          </button>
        </div>
        <p>
          <Link to="/documents">Back to documents</Link>
        </p>
      </section>
    );
  }

  const timeline = timelineQuery.data;
  const pipeline = derivePipelineView(timeline);
  const pathDecisions = collectPathDecisions(timeline);

  return (
    <section aria-labelledby="document-heading">
      <p className="eyebrow">Document</p>
      <div className="page-heading">
        <h1 id="document-heading">{timeline.document.file_name}</h1>
        <DeleteDocumentControl
          fileName={timeline.document.file_name}
          busy={deleteMutation.isPending}
          onDelete={async () => {
            await deleteMutation.mutateAsync();
          }}
        />
      </div>
      <p className="visually-hidden" role="status">
        {timelineSummary(timeline)}
      </p>
      {refreshError ? <p role="status">{refreshError}</p> : null}
      <dl className="meta-list">
        <div>
          <dt>Status</dt>
          <dd data-status={timeline.document.status}>{statusLabel(timeline.document.status)}</dd>
        </div>
        <div>
          <dt>Extraction mode</dt>
          <dd>{extractionModeLabel(timeline.document.extraction_mode)}</dd>
        </div>
        <div>
          <dt>Size</dt>
          <dd>{formatBytes(timeline.document.size_bytes)}</dd>
        </div>
        {timeline.document.reason ? (
          <div>
            <dt>Reason</dt>
            <dd>{timeline.document.reason}</dd>
          </div>
        ) : null}
      </dl>
      <h2>Stages</h2>
      <StageStepper view={pipeline} />
      <h2>Path decisions</h2>
      <p className="lede compact">
        Full escalation after Route/Extract: declined tools, skipped Docling when unset, then
        Gemini/OpenAI — or an explicit <code>chain_exhausted</code> when nothing further is
        configured.
      </p>
      <PathDecisions decisions={pathDecisions} />
      <h2>Extraction attempts</h2>
      {timeline.attempts.length === 0 ? (
        <p>No extraction attempts yet.</p>
      ) : (
        <div className="table-wrap">
          <table>
            <caption>Extraction attempts for {timeline.document.file_name}</caption>
            <thead>
              <tr>
                <th scope="col">Path</th>
                <th scope="col">Extractor</th>
                <th scope="col">Attempt</th>
                <th scope="col">Decision</th>
                <th scope="col">Status</th>
                <th scope="col">Cost</th>
                <th scope="col">Reason</th>
              </tr>
            </thead>
            <tbody>
              {timeline.attempts.map((attempt) => (
                <tr key={attempt.id}>
                  <td>{extractorPathLabel(extractorPathFor(attempt.extractor))}</td>
                  <td>
                    {attempt.extractor} {attempt.extractor_version}
                  </td>
                  <td>{String(attempt.attempt)}</td>
                  <td>
                    {attempt.decision ? pathDecisionKindLabel(attempt.decision) : "—"}
                  </td>
                  <td>{attemptStatusLabel(attempt.status)}</td>
                  <td>{formatCostMicros(attempt.cost_micros)}</td>
                  <td>
                    {attempt.reason_code || attempt.reason_message || attempt.error ? (
                      <>
                        {attempt.reason_message ?? attempt.error ?? "None"}
                        {attempt.reason_code ? (
                          <>
                            {" "}
                            <code>{attempt.reason_code}</code>
                          </>
                        ) : null}
                      </>
                    ) : (
                      "None"
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ExtractedContentSection
        documentId={timeline.document.id}
        documentStatus={timeline.document.status}
        fileName={timeline.document.file_name}
        pathDecisions={pathDecisions}
      />
      <p>
        <Link to="/documents">Back to documents</Link>
      </p>
    </section>
  );
}
