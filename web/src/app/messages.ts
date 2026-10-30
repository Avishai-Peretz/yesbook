import { ApiError, InvalidApiResponseError } from "../api/client";

export function describeFailure(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    if (error.status === 401 || error.status === 403) {
      return "You are not authorized to do that.";
    }
    if (error.status === 404) {
      return "That document was not found.";
    }
    if (error.status === 400 || error.status === 409) {
      return error.message;
    }
    return fallback;
  }
  if (error instanceof InvalidApiResponseError) {
    return "The server returned data the app could not read.";
  }
  return fallback;
}
