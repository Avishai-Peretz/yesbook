import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
  MAX_UPLOAD_BYTES,
  documentSchema,
  registrationSchema,
  type Document,
  type Registration,
} from "../../api/contracts";
import type { AppSources } from "../../app/sources";
import { defaultAppSources } from "../../app/sources";
import { renderWithSources } from "../../test/render";
import { UploadExpiredError, UploadFailedError } from "./errors";
import { UploadPage } from "./UploadPage";

const registration: Registration = registrationSchema.parse({
  job: {
    id: "doc-new",
    status: "awaiting_upload",
    file_name: "lesson.pdf",
    size_bytes: 8,
    created_at: "2026-11-02T13:00:00Z",
    updated_at: "2026-11-02T13:00:00Z",
  },
  upload: {
    url: "https://uploads.example.test/doc-new",
    method: "PUT",
    headers: { "Content-Type": "application/pdf" },
    expires_at: "2026-11-02T13:15:00Z",
  },
});

const queued: Document = documentSchema.parse({
  id: "doc-new",
  status: "queued",
  file_name: "lesson.pdf",
  size_bytes: 8,
  created_at: "2026-11-02T13:00:00Z",
  updated_at: "2026-11-02T13:00:01Z",
});

const pdf = new File(["%PDF-1.7"], "lesson.pdf", { type: "application/pdf" });

function renderUpload(uploads: AppSources["uploads"]) {
  return renderWithSources(<UploadPage />, {
    sources: {
      ...defaultAppSources,
      uploads,
      timelinePollIntervalMs: 60_000,
      timelinePollMaxIntervalMs: 60_000,
    },
  });
}

async function chooseFile(user: ReturnType<typeof userEvent.setup>, file: File) {
  await user.upload(screen.getByLabelText("PDF file"), file);
}

describe("Upload", () => {
  it("returns focus to the file control for an unsupported file", async () => {
    const user = userEvent.setup();
    renderUpload({
      register: () => Promise.reject(new Error("should not register")),
      upload: () => Promise.reject(new Error("should not upload")),
      submit: () => Promise.reject(new Error("should not submit")),
    });
    const input = screen.getByLabelText("PDF file");
    fireEvent.change(input, {
      target: { files: [new File(["hello"], "notes.txt", { type: "text/plain" })] },
    });
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Only PDF files can be processed.");
    expect(input).toHaveFocus();
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAttribute("aria-describedby", "document-file-hint document-file-error");
  });

  it("rejects an empty or oversized PDF", async () => {
    const user = userEvent.setup();
    renderUpload({
      register: () => Promise.reject(new Error("should not register")),
      upload: () => Promise.reject(new Error("should not upload")),
      submit: () => Promise.reject(new Error("should not submit")),
    });
    await chooseFile(user, new File([], "empty.pdf", { type: "application/pdf" }));
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    expect(screen.getByRole("alert")).toHaveTextContent("The PDF is empty.");

    const oversized = new File(["%PDF"], "big.pdf", { type: "application/pdf" });
    Object.defineProperty(oversized, "size", { value: MAX_UPLOAD_BYTES + 1 });
    await chooseFile(user, oversized);
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    expect(screen.getByRole("alert")).toHaveTextContent("The PDF must be 100 MB or smaller.");
  });

  it("submits a PDF from the keyboard and reports upload progress", async () => {
    const user = userEvent.setup();
    let releaseUpload: () => void = () => undefined;
    let registeredMode: string | undefined;
    let submittedMode: string | undefined;
    renderUpload({
      register: (input) => {
        registeredMode = input.extraction_mode;
        return Promise.resolve(registration);
      },
      upload: (_target, _file, _signal, onProgress) => {
        onProgress(40, 100);
        return new Promise((resolve) => {
          releaseUpload = () => {
            resolve();
          };
        });
      },
      submit: (_id, _signal, input) => {
        submittedMode = input?.extraction_mode;
        return Promise.resolve(queued);
      },
    });
    const input = screen.getByLabelText("PDF file");
    await chooseFile(user, pdf);
    await user.click(screen.getByRole("radio", { name: "Pinned: Gemini" }));
    input.focus();
    await user.tab();
    // Radios sit between the file input and submit; click submit directly.
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    expect(await screen.findByRole("progressbar", { name: "Upload progress" })).toHaveAttribute(
      "value",
      "40",
    );
    expect(screen.getByText("40 of 100 bytes")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Uploading lesson.pdf.");
    releaseUpload();
    expect(await screen.findByRole("link", { name: "View progress" })).toHaveAttribute(
      "href",
      "/documents/doc-new",
    );
    expect(screen.getByRole("status")).toHaveTextContent("Processing is queued.");
    expect(registeredMode).toBe("gemini");
    expect(submittedMode).toBe("gemini");
  });

  it("retries an expired upload link", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    renderUpload({
      register: () => Promise.resolve(registration),
      upload: () => {
        attempts += 1;
        if (attempts === 1) return Promise.reject(new UploadExpiredError());
        return Promise.resolve();
      },
      submit: () => Promise.resolve(queued),
    });
    await chooseFile(user, pdf);
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The upload link expired.");
    await user.click(screen.getByRole("button", { name: "Retry upload" }));
    expect(await screen.findByRole("link", { name: "View progress" })).toBeInTheDocument();
    expect(attempts).toBe(2);
  });

  it("retries a failed upload", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    renderUpload({
      register: () => Promise.resolve(registration),
      upload: () => {
        attempts += 1;
        if (attempts === 1) return Promise.reject(new UploadFailedError());
        return Promise.resolve();
      },
      submit: () => Promise.resolve(queued),
    });
    await chooseFile(user, pdf);
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The upload did not finish. Retry.");
    await user.click(screen.getByRole("button", { name: "Retry upload" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Processing is queued.");
  });

  it("cancels an in-flight upload", async () => {
    const user = userEvent.setup();
    let submitted = false;
    let uploadSignal: AbortSignal | undefined;
    renderUpload({
      register: () => Promise.resolve(registration),
      upload: (_target, _file, signal) => {
        uploadSignal = signal;
        return new Promise((_resolve, reject) => {
          signal.addEventListener("abort", () => {
            reject(new DOMException("The operation was aborted.", "AbortError"));
          });
        });
      },
      submit: () => {
        submitted = true;
        return Promise.resolve(queued);
      },
    });
    await chooseFile(user, pdf);
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    await user.click(await screen.findByRole("button", { name: "Cancel upload" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Upload cancelled.");
    expect(uploadSignal?.aborted).toBe(true);
    expect(submitted).toBe(false);
  });

  it("asks for a file before uploading", async () => {
    const user = userEvent.setup();
    renderUpload({
      register: () => Promise.reject(new Error("should not register")),
      upload: () => Promise.reject(new Error("should not upload")),
      submit: () => Promise.reject(new Error("should not submit")),
    });
    await user.click(screen.getByRole("button", { name: "Upload PDF" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Choose a PDF to upload.");
    expect(screen.getByLabelText("PDF file")).toHaveFocus();
  });
});
