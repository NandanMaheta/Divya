/**
 * Divya design tokens: the single source for colours, radii and spacing.
 * theme.css mirrors these values for Tailwind v4 (admin) and Uniwind (client); keep both in sync.
 * Placeholder palette until the Stage 1 design direction is approved.
 */
export const colors = {
  brand: { 50: "#fff7ed", 500: "#c2410c", 600: "#9a3412", 900: "#431407" },
  ink: { 900: "#111827", 600: "#4b5563", 400: "#9ca3af" },
  surface: { base: "#ffffff", muted: "#f8fafc", dark: "#0b0f17" },
  tone: {
    amber: "#d97706",
    blue: "#2563eb",
    green: "#16a34a",
    red: "#dc2626",
    grey: "#6b7280",
  },
} as const;

export const radius = { sm: 6, md: 10, lg: 16, full: 9999 } as const;
export const space = { 1: 4, 2: 8, 3: 12, 4: 16, 6: 24, 8: 32 } as const;
