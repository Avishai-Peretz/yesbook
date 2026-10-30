import { useQuery } from "@tanstack/react-query";
import {
  isPublishedDocumentStatus,
  type DocumentStatus,
} from "../../api/contracts";
import { describeFailure } from "../../app/messages";
import { useAppSources } from "../../app/sources";
import type { PathDecisionView } from "../documents/model";
import { ContentViewer, type ContentViewerState } from "./ContentViewer";

interface ExtractedContentSectionProps {
  documentId: string;
  documentStatus: DocumentStatus;
  fileName: string;
  pathDecisions?: readonly PathDecisionView[];
}

function unavailableMessage(status: DocumentStatus): string {
  switch (status) {
    case "blocked":
      return "This document is blocked, so no extracted content was published.";
    case "processing":
    case "queued":
    case "awaiting_upload":
      return "Extracted content will appear when processing finishes.";
    default:
      return "Extracted content is not available for this document.";
  }
}

export function ExtractedContentSection({
  documentId,
  documentStatus,
  fileName,
  pathDecisions = [],
}: ExtractedContentSectionProps) {
  const sources = useAppSources();
  const published = isPublishedDocumentStatus(documentStatus);

  const contentQuery = useQuery({
    queryKey: ["content", documentId],
    enabled: published,
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: ({ signal }) =>
      sources.documents.getContent(documentId, { includeReview: true }, signal),
  });

  let state: ContentViewerState;
  if (!published) {
    state = { status: "unavailable", message: unavailableMessage(documentStatus) };
  } else if (contentQuery.isPending) {
    state = { status: "loading" };
  } else if (contentQuery.isError) {
    state = {
      status: "error",
      message: describeFailure(
        contentQuery.error,
        "The extracted content could not be loaded. Retry.",
      ),
      onRetry: () => {
        void contentQuery.refetch();
      },
    };
  } else if (contentQuery.data.content.blocks.length === 0) {
    state = { status: "empty", content: contentQuery.data.content };
  } else {
    state = { status: "ready", response: contentQuery.data };
  }

  return (
    <ContentViewer state={state} fileName={fileName} pathDecisions={pathDecisions} />
  );
}
