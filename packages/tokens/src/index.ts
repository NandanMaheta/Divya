/**
 * Swastik design tokens: the single source for colour, type, spacing, radius, elevation and motion.
 * theme.css mirrors these values for Tailwind v4 (admin) and Uniwind (client); keep both in sync.
 * Design direction and contrast ratios: docs/design/design-direction.md.
 *
 * Colour roles are semantic and keep the same name in light and dark mode:
 *   brand[600] = primary action fill and brand text   onBrand  = text/icons on brand[600]
 *   brand[500] = focus ring, hover                     brand[50] = soft selected background
 *   brand[900] = text on brand[50]
 *   ink[900/600/400] = primary / secondary / tertiary text (all >= 4.5:1 on every surface)
 *   surface.base = cards and panels, surface.muted = app canvas, surface.sunken = wells, inputs, table headers
 *   tone[x] = status text and icon colour, toneSoft[x] = status chip background
 * Status tones (see @swastik/domain statusTone): amber = to do, blue = waiting, green = done or allotted,
 * red = missed or overdue, grey = not allotted. Never show a tone without its icon and label.
 */

export type ColorScheme = "light" | "dark";
export type ToneName = "amber" | "blue" | "green" | "red" | "grey";

export interface Palette {
  readonly brand: {
    readonly 50: string;
    readonly 500: string;
    readonly 600: string;
    readonly 900: string;
  };
  readonly onBrand: string;
  readonly gold: string;
  readonly ink: { readonly 900: string; readonly 600: string; readonly 400: string };
  readonly surface: {
    readonly base: string;
    readonly muted: string;
    readonly sunken: string;
    readonly dark: string;
  };
  readonly line: { readonly subtle: string; readonly strong: string };
  readonly tone: Readonly<Record<ToneName, string>>;
  readonly toneSoft: Readonly<Record<ToneName, string>>;
  readonly scrim: string;
}

/** Light palette. Exported as `colors` for backwards compatibility. */
export const colors = {
  brand: { 50: "#f5effa", 500: "#6e3aa0", 600: "#5b2a86", 900: "#2e1245" },
  onBrand: "#ffffff",
  gold: "#b07a1e",
  ink: { 900: "#1c1917", 600: "#57534e", 400: "#6f6863" },
  surface: { base: "#ffffff", muted: "#faf8f5", sunken: "#f2efea", dark: "#141210" },
  line: { subtle: "#e4e0da", strong: "#8a837c" },
  tone: {
    amber: "#a14f00",
    blue: "#1d5fc2",
    green: "#1a7034",
    red: "#b42318",
    grey: "#5c5853",
  },
  toneSoft: {
    amber: "#fdf1dc",
    blue: "#e6effc",
    green: "#e3f4e8",
    red: "#fce8e6",
    grey: "#efedea",
  },
  scrim: "rgba(20, 18, 16, 0.5)",
} as const satisfies Palette;

/** Dark palette: same roles, values tuned for warm near-black surfaces. */
export const darkColors = {
  brand: { 50: "#2a1a3a", 500: "#b48ce0", 600: "#c4a3e8", 900: "#e9dcf7" },
  onBrand: "#2e1245",
  gold: "#d9a441",
  ink: { 900: "#f5f2ee", 600: "#c9c3bc", 400: "#a39c94" },
  surface: { base: "#1f1c19", muted: "#141210", sunken: "#2a2622", dark: "#141210" },
  line: { subtle: "#34302b", strong: "#7a736c" },
  tone: {
    amber: "#f2b755",
    blue: "#8db8f5",
    green: "#7fcf95",
    red: "#f59a90",
    grey: "#a39c94",
  },
  toneSoft: {
    amber: "#3a2a10",
    blue: "#14263f",
    green: "#123321",
    red: "#3d1714",
    grey: "#2a2622",
  },
  scrim: "rgba(0, 0, 0, 0.6)",
} as const satisfies Palette;

export const themes: Readonly<Record<ColorScheme, Palette>> = { light: colors, dark: darkColors };

export const radius = { sm: 6, md: 10, lg: 16, xl: 24, full: 9999 } as const;

export const space = {
  0: 0,
  1: 4,
  2: 8,
  3: 12,
  4: 16,
  5: 20,
  6: 24,
  8: 32,
  10: 40,
  12: 48,
  16: 64,
} as const;

/** Minimum touch target in dp/pt (constitution VII); client controls use 48. */
export const touchTarget = { min: 44, comfortable: 48 } as const;

/**
 * Font families. The client loads the Noto Sans files under these names; the web stack lives in theme.css.
 * Noto Sans, Noto Sans Devanagari and Noto Sans Gujarati share metrics and stroke, so mixed-script text stays even.
 */
export const fontFamily = {
  regular: "NotoSans-Regular",
  medium: "NotoSans-Medium",
  semibold: "NotoSans-SemiBold",
  bold: "NotoSans-Bold",
  web: '"Noto Sans", "Noto Sans Devanagari", "Noto Sans Gujarati", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
} as const;

export interface TypeStyle {
  readonly fontSize: number;
  readonly lineHeight: number;
  readonly fontWeight: "400" | "500" | "600" | "700";
  readonly letterSpacing: number;
}

/** Type scale at 100% system text size (sp / pt). Sizes scale with dynamic type; never fix them. */
export const typography = {
  amount: { fontSize: 40, lineHeight: 48, fontWeight: "700", letterSpacing: -0.5 },
  display: { fontSize: 32, lineHeight: 40, fontWeight: "700", letterSpacing: -0.25 },
  title: { fontSize: 24, lineHeight: 32, fontWeight: "600", letterSpacing: 0 },
  heading: { fontSize: 20, lineHeight: 28, fontWeight: "600", letterSpacing: 0 },
  bodyLg: { fontSize: 18, lineHeight: 26, fontWeight: "400", letterSpacing: 0 },
  body: { fontSize: 16, lineHeight: 24, fontWeight: "400", letterSpacing: 0 },
  label: { fontSize: 16, lineHeight: 22, fontWeight: "600", letterSpacing: 0 },
  bodySm: { fontSize: 14, lineHeight: 20, fontWeight: "400", letterSpacing: 0 },
  caption: { fontSize: 13, lineHeight: 18, fontWeight: "500", letterSpacing: 0.1 },
} as const satisfies Record<string, TypeStyle>;

/** Devanagari and Gujarati need taller lines for matras; multiply lineHeight by this when the locale is hi or gu. */
export const indicLineHeightScale = 1.2;

/** Upper bound for maxFontSizeMultiplier on the `amount` style only, so huge totals still fit one line. */
export const amountMaxFontScale = 1.6;

/** React Native shadow presets (iOS/web shadow* plus Android elevation). Dark mode uses surfaces, not shadows. */
export const elevation = {
  0: {
    shadowColor: "#1c1917",
    shadowOpacity: 0,
    shadowRadius: 0,
    shadowOffset: { width: 0, height: 0 },
    elevation: 0,
  },
  1: {
    shadowColor: "#1c1917",
    shadowOpacity: 0.06,
    shadowRadius: 2,
    shadowOffset: { width: 0, height: 1 },
    elevation: 1,
  },
  2: {
    shadowColor: "#1c1917",
    shadowOpacity: 0.1,
    shadowRadius: 12,
    shadowOffset: { width: 0, height: 4 },
    elevation: 4,
  },
  3: {
    shadowColor: "#1c1917",
    shadowOpacity: 0.16,
    shadowRadius: 32,
    shadowOffset: { width: 0, height: 12 },
    elevation: 12,
  },
} as const;

/** Motion: short, calm, never decorative loops. Honour reduce-motion by using duration.instant. */
export const motion = {
  duration: { instant: 0, fast: 120, base: 200, slow: 320 },
  /** Cubic-bezier control points for Reanimated Easing.bezier and CSS. */
  easing: {
    standard: [0.2, 0, 0, 1],
    enter: [0, 0, 0.2, 1],
    exit: [0.4, 0, 1, 1],
  },
  /** Reanimated withSpring config: settles quickly with no visible bounce. */
  spring: { damping: 22, stiffness: 240, mass: 1 },
} as const;
