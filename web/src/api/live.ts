import type { ApiClient } from "./client";
import type {
  ContentQueryOptions,
  DocumentSource,
  MemorySources,
  UploadSource,
} from "../fixtures/source";

export function createLiveDocumentSource(client: ApiClient): DocumentSource {
  return {
    listDocuments(cursor, signal) {
      return client.listDocuments(cursor, signal);
    },
    getTimeline(id, signal) {
      return client.getTimeline(id, signal);
    },
    getContent(id, options: ContentQueryOptions, signal) {
      return client.getContent(id, options, signal);
    },
    deleteDocument(id, signal) {
      return client.deleteDocument(id, signal);
    },
  };
}

export function createLiveUploadSource(client: ApiClient): UploadSource {
  return {
    register(input, signal) {
      return client.register(input, signal);
    },
    upload(target, file, signal, onProgress) {
      return client.upload(target, file, signal, onProgress);
    },
    submit(documentId, signal, input) {
      return client.submit(documentId, signal, input);
    },
  };
}

export function createLiveSources(client: ApiClient): MemorySources {
  return {
    documents: createLiveDocumentSource(client),
    uploads: createLiveUploadSource(client),
  };
}
