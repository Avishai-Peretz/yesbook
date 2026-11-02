import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "./app/App";
import { liveAppSources } from "./app/sources";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 10_000,
      refetchOnWindowFocus: false,
    },
  },
});

const root = document.querySelector<HTMLDivElement>("#root");
if (!root) {
  throw new Error("Application root element was not found.");
}

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App sources={liveAppSources} />
    </QueryClientProvider>
  </StrictMode>,
);
