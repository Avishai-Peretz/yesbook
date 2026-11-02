import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  EXTRACTION_MODES,
  PDF_CONTENT_TYPE,
  type Document,
  type ExtractionMode,
} from "../../api/contracts";
import { isAbortError } from "../../app/abort";
import { describeFailure } from "../../app/messages";
import { useAppSources } from "../../app/sources";
import { extractionModeLabel } from "../documents/model";
import { UploadExpiredError } from "./errors";
import { validateUploadFile } from "./validation";

type UploadPhase =
  | { phase: "ready" }
  | { phase: "invalid"; message: string }
  | { phase: "registering"; fileName: string }
  | { phase: "uploading"; fileName: string; loaded: number; total: number }
  | { phase: "submitting"; fileName: string }
  | { phase: "succeeded"; document: Document }
  | { phase: "expired"; message: string }
  | { phase: "failed"; message: string }
  | { phase: "cancelled" };

function isBusy(phase: UploadPhase): boolean {
  return (
    phase.phase === "registering" ||
    phase.phase === "uploading" ||
    phase.phase === "submitting"
  );
}

function liveMessage(phase: UploadPhase): string {
  switch (phase.phase) {
    case "registering":
      return `Registering ${phase.fileName}…`;
    case "uploading":
      return `Uploading ${phase.fileName}.`;
    case "submitting":
      return `Submitting ${phase.fileName}…`;
    case "succeeded":
      return `Uploaded ${phase.document.file_name}. Processing is queued.`;
    case "cancelled":
      return "Upload cancelled.";
    case "ready":
    case "invalid":
    case "expired":
    case "failed":
      return "";
  }
}

export function UploadPage() {
  const { uploads } = useAppSources();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const attemptRef = useRef(0);
  const [file, setFile] = useState<File | null>(null);
  const [extractionMode, setExtractionMode] = useState<ExtractionMode>("automatic");
  const [phase, setPhase] = useState<UploadPhase>({ phase: "ready" });

  useEffect(() => {
    return () => {
      attemptRef.current += 1;
      abortRef.current?.abort();
    };
  }, []);

  async function begin() {
    const selected = file;
    if (!selected) {
      setPhase({ phase: "invalid", message: "Choose a PDF to upload." });
      fileInputRef.current?.focus();
      return;
    }
    const problem = validateUploadFile(selected);
    if (problem) {
      setPhase({ phase: "invalid", message: problem });
      fileInputRef.current?.focus();
      return;
    }

    const attempt = attemptRef.current + 1;
    attemptRef.current = attempt;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const signal = controller.signal;
    const fileName = selected.name.trim();
    const stillCurrent = () => attemptRef.current === attempt;

    try {
      setPhase({ phase: "registering", fileName });
      const registration = await uploads.register(
        {
          file_name: fileName,
          content_type: PDF_CONTENT_TYPE,
          size_bytes: selected.size,
          extraction_mode: extractionMode,
        },
        signal,
      );
      if (!stillCurrent()) return;
      setPhase({ phase: "uploading", fileName, loaded: 0, total: selected.size });
      await uploads.upload(registration.upload, selected, signal, (loaded, total) => {
        if (!stillCurrent() || signal.aborted) return;
        setPhase({ phase: "uploading", fileName, loaded, total });
      });
      if (!stillCurrent()) return;
      setPhase({ phase: "submitting", fileName });
      // Same selector as register so mode cannot drift before the job leaves awaiting_upload.
      const document = await uploads.submit(registration.job.id, signal, {
        extraction_mode: extractionMode,
      });
      if (!stillCurrent()) return;
      setPhase({ phase: "succeeded", document });
    } catch (error) {
      if (!stillCurrent()) return;
      if (signal.aborted || isAbortError(error)) {
        setPhase({ phase: "cancelled" });
        return;
      }
      if (error instanceof UploadExpiredError) {
        setPhase({
          phase: "expired",
          message: "The upload link expired. Retry to request a new link.",
        });
        return;
      }
      setPhase({
        phase: "failed",
        message: describeFailure(error, "The upload did not finish. Retry."),
      });
    }
  }

  function cancel() {
    attemptRef.current += 1;
    abortRef.current?.abort();
    setPhase({ phase: "cancelled" });
  }

  const message = liveMessage(phase);

  return (
    <section aria-labelledby="upload-heading">
      <p className="eyebrow">Upload</p>
      <h1 id="upload-heading">Upload a PDF</h1>
      <p className="lede">
        Register the file, choose Automatic or pin a tool (pin-then-escalate), then submit.
      </p>
      <form
        className="upload-form"
        noValidate
        aria-busy={isBusy(phase)}
        onSubmit={(event) => {
          event.preventDefault();
          if (!isBusy(phase)) void begin();
        }}
      >
        <div className="field">
          <label htmlFor="document-file">PDF file</label>
          <input
            ref={fileInputRef}
            id="document-file"
            name="file"
            type="file"
            accept="application/pdf,.pdf"
            disabled={isBusy(phase)}
            aria-invalid={phase.phase === "invalid"}
            aria-describedby={
              phase.phase === "invalid"
                ? "document-file-hint document-file-error"
                : "document-file-hint"
            }
            onChange={(event) => {
              setFile(event.currentTarget.files?.[0] ?? null);
              if (phase.phase === "invalid") setPhase({ phase: "ready" });
            }}
          />
          <p id="document-file-hint">PDF files up to 100 MB.</p>
        </div>
        <fieldset className="field extraction-mode-field" disabled={isBusy(phase)}>
          <legend>Extraction mode</legend>
          <p id="extraction-mode-hint" className="field-hint">
            Automatic escalates the chain. Pinning tries that tool first, then escalates.
          </p>
          <div
            className="extraction-mode-options"
            role="radiogroup"
            aria-describedby="extraction-mode-hint"
          >
            {EXTRACTION_MODES.map((mode) => (
              <label key={mode} className="extraction-mode-option">
                <input
                  type="radio"
                  name="extraction_mode"
                  value={mode}
                  checked={extractionMode === mode}
                  onChange={() => {
                    setExtractionMode(mode);
                  }}
                />
                <span>{extractionModeLabel(mode)}</span>
              </label>
            ))}
          </div>
        </fieldset>
        {phase.phase === "invalid" ? (
          <p id="document-file-error" role="alert">
            <span className="visually-hidden">Error: </span>
            {phase.message}
          </p>
        ) : null}
        {phase.phase === "expired" || phase.phase === "failed" ? (
          <p role="alert">
            <span className="visually-hidden">Error: </span>
            {phase.message}
          </p>
        ) : null}
        {phase.phase === "uploading" ? (
          <div className="upload-progress">
            <progress
              aria-label="Upload progress"
              max={phase.total}
              value={phase.loaded}
            />
            <p>
              {String(phase.loaded)} of {String(phase.total)} bytes
            </p>
          </div>
        ) : null}
        {message ? <p role="status">{message}</p> : null}
        {phase.phase === "succeeded" ? (
          <p>
            <Link to={`/documents/${phase.document.id}`}>View progress</Link>
          </p>
        ) : null}
        <div className="actions">
          <button type="submit" disabled={isBusy(phase)}>
            {phase.phase === "expired" || phase.phase === "failed" ? "Retry upload" : "Upload PDF"}
          </button>
          {isBusy(phase) ? (
            <button type="button" className="secondary" onClick={cancel}>
              Cancel upload
            </button>
          ) : null}
        </div>
      </form>
    </section>
  );
}
