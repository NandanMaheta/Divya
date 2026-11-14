import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Link, Outlet } from "@tanstack/react-router";

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: RootLayout,
});

const nav = [{ to: "/", label: "Today" }] as const;

function RootLayout() {
  return (
    <div className="flex min-h-screen bg-surface-muted text-ink-900">
      <aside className="w-56 border-r border-ink-400/30 bg-surface-base p-4">
        <p className="mb-6 text-lg font-semibold text-brand-600">Swastik Admin</p>
        <nav className="flex flex-col gap-1">
          {nav.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className="rounded-md px-3 py-2 hover:bg-brand-50 [&.active]:bg-brand-50 [&.active]:font-medium"
            >
              {item.label}
            </Link>
          ))}
        </nav>
      </aside>
      <main className="flex-1 p-8">
        <Outlet />
      </main>
    </div>
  );
}
