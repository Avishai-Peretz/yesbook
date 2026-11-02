import { createContext, useContext } from "react";
import { apiClient } from "../api/client";
import { createLiveSources } from "../api/live";
import { createMemorySources, type DocumentSource, type UploadSource } from "../fixtures/source";

export const DEFAULT_TIMELINE_POLL_INTERVAL_MS = 2_000;
export const DEFAULT_TIMELINE_POLL_MAX_INTERVAL_MS = 16_000;

export interface AppSources {
  documents: DocumentSource;
  uploads: UploadSource;
  timelinePollIntervalMs: number;
  timelinePollMaxIntervalMs: number;
}

const memory = createMemorySources();
const live = createLiveSources(apiClient);

/** Fixture-backed sources used by unit tests and offline demos. */
export const fixtureAppSources: AppSources = {
  documents: memory.documents,
  uploads: memory.uploads,
  timelinePollIntervalMs: DEFAULT_TIMELINE_POLL_INTERVAL_MS,
  timelinePollMaxIntervalMs: DEFAULT_TIMELINE_POLL_MAX_INTERVAL_MS,
};

/** Live HTTP sources talking to yesbook-dev through the Vite `/api` proxy. */
export const liveAppSources: AppSources = {
  documents: live.documents,
  uploads: live.uploads,
  timelinePollIntervalMs: DEFAULT_TIMELINE_POLL_INTERVAL_MS,
  timelinePollMaxIntervalMs: DEFAULT_TIMELINE_POLL_MAX_INTERVAL_MS,
};

/** Alias kept for tests that inject or extend the fixture backend. */
export const defaultAppSources = fixtureAppSources;

export const AppSourcesContext = createContext<AppSources>(fixtureAppSources);

export function useAppSources(): AppSources {
  return useContext(AppSourcesContext);
}
