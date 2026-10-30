import { useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { useAppSources } from "../../app/sources";
import { DeleteDocumentControl } from "./DeleteDocumentControl";
import { formatBytes, formatTimestamp } from "./format";
import { statusLabel, toDocumentListState } from "./model";

export function DocumentListPage() {
  const sources = useAppSources();
  const queryClient = useQueryClient();
  const listQuery = useInfiniteQuery({
    queryKey: ["documents"],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) => sources.documents.listDocuments(pageParam, signal),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const state = toDocumentListState({
    pages: listQuery.data?.pages,
    hasNextPage: listQuery.hasNextPage,
    isFetchingNextPage: listQuery.isFetchingNextPage,
    initialError: listQuery.data === undefined && listQuery.isError ? listQuery.error : null,
    nextPageError: listQuery.isFetchNextPageError ? listQuery.error : null,
  });
  const [moreAnnouncement, setMoreAnnouncement] = useState("");

  return (
    <section aria-labelledby="documents-heading">
      <p className="eyebrow">Workspace</p>
      <div className="page-heading">
        <h1 id="documents-heading">Documents</h1>
        <Link className="button-link" to="/documents/new">
          Upload a PDF
        </Link>
      </div>
      {state.status === "loading" ? (
        <p role="status">Loading documents…</p>
      ) : null}
      {state.status === "error" ? (
        <div className="empty-state" role="alert">
          <h2>Documents are unavailable</h2>
          <p>{state.message}</p>
          <button
            type="button"
            onClick={() => {
              void listQuery.refetch();
            }}
          >
            Retry
          </button>
        </div>
      ) : null}
      {state.status === "empty" ? (
        <div className="empty-state">
          <h2>No documents yet</h2>
          <p>Upload a PDF to start processing.</p>
          <Link className="button-link" to="/documents/new">
            Upload a PDF
          </Link>
        </div>
      ) : null}
      {state.status === "ready" ? (
        <>
          <p className="visually-hidden" role="status">
            {moreAnnouncement || `${String(state.documents.length)} documents`}
          </p>
          <ul className="document-list" aria-busy={state.loadingMore}>
            {state.documents.map((document) => (
              <li key={document.id}>
                <div className="document-row">
                  <Link to={`/documents/${document.id}`}>
                    <span>{document.file_name}</span>
                    <span className="document-status" data-status={document.status}>
                      {statusLabel(document.status)}
                    </span>
                  </Link>
                  <DeleteDocumentControl
                    fileName={document.file_name}
                    onDelete={async () => {
                      await sources.documents.deleteDocument(
                        document.id,
                        new AbortController().signal,
                      );
                      queryClient.removeQueries({ queryKey: ["timeline", document.id] });
                      queryClient.removeQueries({ queryKey: ["content", document.id] });
                      await queryClient.invalidateQueries({ queryKey: ["documents"] });
                    }}
                  />
                </div>
                <p className="document-meta">
                  {formatBytes(document.size_bytes)} · Updated {formatTimestamp(document.updated_at)}
                </p>
              </li>
            ))}
          </ul>
          {state.moreError ? (
            <div className="inline-error" role="alert">
              <p>{state.moreError}</p>
              <button
                type="button"
                onClick={() => {
                  void listQuery.fetchNextPage();
                }}
              >
                Retry
              </button>
            </div>
          ) : null}
          {state.hasMore ? (
            <button
              type="button"
              disabled={state.loadingMore}
              onClick={() => {
                setMoreAnnouncement("Loading more documents…");
                void listQuery.fetchNextPage().then(
                  () => {
                    setMoreAnnouncement("Loaded more documents.");
                  },
                  () => {
                    setMoreAnnouncement("The next page could not be loaded.");
                  },
                );
              }}
            >
              {state.loadingMore ? "Loading more…" : "Load more documents"}
            </button>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
