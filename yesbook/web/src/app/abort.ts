export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

export function isAborted(signal: AbortSignal): boolean {
  return signal.aborted;
}

export function throwIfAborted(signal: AbortSignal): void {
  if (signal.aborted) {
    throw new DOMException("The operation was aborted.", "AbortError");
  }
}

export function abortError(): DOMException {
  return new DOMException("The operation was aborted.", "AbortError");
}
