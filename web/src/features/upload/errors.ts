export class UploadExpiredError extends Error {
  constructor() {
    super("The upload link expired.");
    this.name = "UploadExpiredError";
  }
}

export class UploadFailedError extends Error {
  constructor() {
    super("The file could not be uploaded.");
    this.name = "UploadFailedError";
  }
}
