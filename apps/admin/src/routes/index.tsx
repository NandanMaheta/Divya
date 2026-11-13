import { formatRupees, paise } from "@divya/domain";
import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/")({ component: TodayPage });

/** Placeholder for the "Today" dashboard (built in module M12). */
export function TodayPage() {
  return (
    <section>
      <h1 className="text-2xl font-semibold">Today</h1>
      <p className="mt-2 text-ink-600">
        Approvals, open IPOs, allotments and payments will appear here.
      </p>
      <p className="mt-6 text-sm text-ink-400">Formatting check: {formatRupees(paise(8658000))}</p>
    </section>
  );
}
