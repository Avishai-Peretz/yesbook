import { useQuery } from "@tanstack/react-query";
import { Link, NavLink, Outlet, RouterProvider, createBrowserRouter, useLocation } from "react-router-dom";
import { apiClient } from "../api/client";
import { AppSourcesContext, fixtureAppSources, type AppSources } from "./sources";
import { DocumentDetailPage } from "../features/documents/DocumentDetailPage";
import { DocumentListPage } from "../features/documents/DocumentListPage";
import { UploadPage } from "../features/upload/UploadPage";

function AppShell() {
  const { pathname } = useLocation();
  const onDocuments =
    pathname === "/documents" ||
    (pathname.startsWith("/documents/") && pathname !== "/documents/new");
  const onUpload = pathname === "/documents/new";

  return (
    <>
      <a className="skip-link" href="#main-content">
        Skip to main content
      </a>
      <header className="site-header">
        <div className="shell header-content">
          <NavLink className="brand" to="/" aria-label="Yesbook home" end>
            Yesbook
          </NavLink>
          <nav aria-label="Primary navigation">
            <Link
              to="/documents"
              className={onDocuments ? "active" : ""}
              {...(onDocuments ? { "aria-current": "page" as const } : {})}
            >
              Documents
            </Link>
            <Link
              to="/documents/new"
              className={onUpload ? "active" : ""}
              {...(onUpload ? { "aria-current": "page" as const } : {})}
            >
              Upload
            </Link>
          </nav>
        </div>
      </header>
      <main className="shell main-content" id="main-content" tabIndex={-1}>
        <Outlet />
      </main>
    </>
  );
}

function HomePage() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: async ({ signal }) => {
      await apiClient.health(signal);
      return true;
    },
    retry: 1,
    refetchInterval: 30_000,
  });

  return (
    <section className="hero" aria-labelledby="welcome-heading">
      <p className="eyebrow">Document processing</p>
      <h1 id="welcome-heading">Production pipeline console</h1>
      <p className="lede">
        Upload, follow, and review documents as they move through the Yesbook
        pipeline.
      </p>
      <div className="status-card" aria-live="polite">
        <span
          className={`status-dot ${health.isSuccess ? "online" : ""}`}
          aria-hidden="true"
        />
        <div>
          <h2>Local API</h2>
          {health.isPending && <p>Checking availability…</p>}
          {health.isSuccess && <p>Available</p>}
          {health.isError && (
            <p>
              Unavailable. Start <code>yesbook-dev</code> on port 8080 and try
              again.
            </p>
          )}
        </div>
      </div>
    </section>
  );
}

function NotFoundPage() {
  return (
    <section aria-labelledby="not-found-heading">
      <h1 id="not-found-heading">Page not found</h1>
      <p>The page you requested does not exist.</p>
      <NavLink to="/">Return home</NavLink>
    </section>
  );
}

const router = createBrowserRouter([
  {
    element: <AppShell />,
    children: [
      { index: true, element: <HomePage /> },
      { path: "documents", element: <DocumentListPage /> },
      { path: "documents/new", element: <UploadPage /> },
      { path: "documents/:documentId", element: <DocumentDetailPage /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);

export function App({ sources = fixtureAppSources }: { sources?: AppSources }) {
  return (
    <AppSourcesContext.Provider value={sources}>
      <RouterProvider router={router} />
    </AppSourcesContext.Provider>
  );
}
