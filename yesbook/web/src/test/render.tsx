import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, type RenderResult } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter } from "react-router-dom";
import { AppSourcesContext, defaultAppSources, type AppSources } from "../app/sources";

export function renderWithSources(
  ui: ReactElement,
  options?: { sources?: AppSources; path?: string },
): RenderResult {
  const sources = options?.sources ?? defaultAppSources;
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: Infinity },
    },
  });
  return render(
    <QueryClientProvider client={client}>
      <AppSourcesContext.Provider value={sources}>
        <MemoryRouter initialEntries={[options?.path ?? "/"]}>{ui}</MemoryRouter>
      </AppSourcesContext.Provider>
    </QueryClientProvider>,
  );
}
