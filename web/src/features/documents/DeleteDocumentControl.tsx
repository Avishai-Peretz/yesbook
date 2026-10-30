import { useId, useState } from "react";
import { describeFailure } from "../../app/messages";

interface DeleteDocumentControlProps {
  fileName: string;
  busy?: boolean;
  onDelete: () => Promise<void>;
  onDeleted?: () => void;
}

export function DeleteDocumentControl({
  fileName,
  busy = false,
  onDelete,
  onDeleted,
}: DeleteDocumentControlProps) {
  const titleId = useId();
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function confirmDelete() {
    setDeleting(true);
    setError(null);
    try {
      await onDelete();
      setConfirming(false);
      onDeleted?.();
    } catch (reason) {
      setError(describeFailure(reason, "The document could not be deleted. Retry."));
    } finally {
      setDeleting(false);
    }
  }

  if (confirming) {
    return (
      <div
        className="delete-confirm"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <p id={titleId}>
          Permanently delete <strong>{fileName}</strong>? This cannot be undone.
        </p>
        {error ? (
          <p role="alert">
            <span className="visually-hidden">Error: </span>
            {error}
          </p>
        ) : null}
        <div className="actions">
          <button type="button" disabled={deleting || busy} onClick={() => void confirmDelete()}>
            {deleting ? "Deleting…" : "Delete permanently"}
          </button>
          <button
            type="button"
            className="secondary"
            disabled={deleting}
            onClick={() => {
              setConfirming(false);
              setError(null);
            }}
          >
            Cancel
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="delete-control">
      {error ? (
        <p role="alert">
          <span className="visually-hidden">Error: </span>
          {error}
        </p>
      ) : null}
      <button
        type="button"
        className="secondary danger"
        disabled={busy || deleting}
        onClick={() => {
          setError(null);
          setConfirming(true);
        }}
      >
        Delete document
      </button>
    </div>
  );
}
