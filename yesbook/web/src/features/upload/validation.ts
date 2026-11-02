import { MAX_FILE_NAME_LENGTH, MAX_UPLOAD_BYTES, PDF_CONTENT_TYPE } from "../../api/contracts";

export interface UploadFileMetadata {
  name: string;
  type: string;
  size: number;
}

function isPdf(file: UploadFileMetadata): boolean {
  if (file.type === PDF_CONTENT_TYPE) return true;
  const name = file.name.trim().toLowerCase();
  return name.endsWith(".pdf") && (file.type === "" || file.type === "application/octet-stream");
}

export function validateUploadFile(file: UploadFileMetadata): string | null {
  const name = file.name.trim();
  if (!isPdf(file)) return "Only PDF files can be processed.";
  if (name.length === 0) return "The file name is empty.";
  if (new TextEncoder().encode(name).length > MAX_FILE_NAME_LENGTH) {
    return "The file name must be 255 bytes or fewer.";
  }
  if (file.size <= 0) return "The PDF is empty.";
  if (file.size > MAX_UPLOAD_BYTES) return "The PDF must be 100 MB or smaller.";
  return null;
}
