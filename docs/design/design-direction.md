# Divya design direction (Stage 1)

Status: **proposal for owner approval**. Tokens live in `packages/tokens/src/index.ts` and `packages/tokens/src/theme.css` (kept in sync). Screen specs live in `docs/design/screens/`, UX copy in `docs/design/copy/`.

Sources: constitution principle VII, `docs/product/business-rules.md`, `docs/product/product-blueprint.md` sections 2–4 and 7.

---

## 1. Brand personality

Divya means "divine, radiant". We take the warmth, not the ornament. Clients send real money because of what this app tells them, so the app has to feel like a careful family accountant: calm, exact and kind.

| We are | We are not |
| --- | --- |
| Calm: one clear next step per screen, no alarms unless something is truly late | Urgent-by-default, red badges everywhere |
| Trustworthy: every amount shows how it was worked out | A black box that just says "Pay ₹17,460" |
| Plain-spoken: "You pay", "We pay you", "Mark applied" | Finance jargon such as "net receivable", "P&L", "consideration" |
| Warm: ivory paper, a deep jamun purple, a touch of diya gold on festival days | A neon "AI dashboard": gradients, glassmorphism, charts for their own sake |
| Big and legible: amounts are the largest thing on screen | Dense tables on the phone |

**Visual idea: "ledger paper and lamplight."** Warm off-white surfaces like a good notebook. Ink-dark warm text. A single deep purple brand colour (jamun) for actions and identity. Gold (diya) is reserved for the logo mark and festival moments and never carries meaning. Status colours are the only bright colours on screen, so a glance at Home tells you what needs you.

**Why purple?** All five status hues (amber, blue, green, red, grey) carry meaning. A saffron or orange brand would be confused with amber "to do", a blue brand with "waiting", a maroon brand with "missed". Jamun purple is the one rich, warm, Indian-feeling hue that collides with none of them.

---

## 2. Colour

### 2.1 Roles

Colour tokens are **semantic**: the same name is used in light and dark mode, and only the value changes. Code never checks the mode to choose a colour; it asks for the role.

| Role (token) | Use |
| --- | --- |
| `brand.600` | Primary button fill, links, active tab, brand text |
| `onBrand` | Text and icons on `brand.600` |
| `brand.500` | Focus ring (2 px, 2 px offset), pressed/hover tint |
| `brand.50` | Selected row, selected chip, hover background (admin) |
| `brand.900` | Text on `brand.50` |
| `gold` | Logo mark, festival banner hairline and motif. Decorative only. |
| `ink.900` / `ink.600` / `ink.400` | Primary / secondary / tertiary text. All three pass 4.5:1 on every surface, so none of them is "too light for text". |
| `surface.muted` | App canvas (page background) |
| `surface.base` | Cards, panels, sheets, admin sidebar |
| `surface.sunken` | Inputs, table headers, the breakdown "well" inside a card |
| `surface.dark` | Always-dark areas (splash). Same value in both modes. |
| `line.subtle` | Card borders and dividers (decorative, no contrast requirement) |
| `line.strong` | Input borders, checkbox outlines, any border that shows where a control is (≥ 3:1) |
| `tone.<name>` | Status text and icons |
| `toneSoft.<name>` | Status chip and banner backgrounds |
| `scrim` | Behind modals and bottom sheets |

### 2.2 Palette

| Token | Light | Dark |
| --- | --- | --- |
| brand.50 | `#F5EFFA` | `#2A1A3A` |
| brand.500 | `#6E3AA0` | `#B48CE0` |
| brand.600 | `#5B2A86` | `#C4A3E8` |
| brand.900 | `#2E1245` | `#E9DCF7` |
| onBrand | `#FFFFFF` | `#2E1245` |
| gold | `#B07A1E` | `#D9A441` |
| ink.900 | `#1C1917` | `#F5F2EE` |
| ink.600 | `#57534E` | `#C9C3BC` |
| ink.400 | `#6F6863` | `#A39C94` |
| surface.base | `#FFFFFF` | `#1F1C19` |
| surface.muted | `#FAF8F5` | `#141210` |
| surface.sunken | `#F2EFEA` | `#2A2622` |
| surface.dark | `#141210` | `#141210` |
| line.subtle | `#E4E0DA` | `#34302B` |
| line.strong | `#8A837C` | `#7A736C` |
| scrim | `rgba(20,18,16,.5)` | `rgba(0,0,0,.6)` |

Neutrals are warm stone greys (a slight red-yellow cast) so the app feels like paper, not a spreadsheet. Dark mode uses warm near-black, never pure `#000`, which reduces glare and OLED smearing.

### 2.3 Status tones

The vocabulary is fixed in `@divya/domain` `statusTone` and used identically in both apps:

| Status | Tone | Icon (Lucide) | Light fg / soft | Dark fg / soft |
| --- | --- | --- | --- | --- |
| To do | amber | `circle-dot` | `#A14F00` / `#FDF1DC` | `#F2B755` / `#3A2A10` |
| Waiting (applied, pending approval, payment claimed) | blue | `hourglass` | `#1D5FC2` / `#E6EFFC` | `#8DB8F5` / `#14263F` |
| Done, allotted, settled | green | `circle-check` | `#1A7034` / `#E3F4E8` | `#7FCF95` / `#123321` |
| Missed, overdue | red | `circle-alert` | `#B42318` / `#FCE8E6` | `#F59A90` / `#3D1714` |
| Not allotted, not needed | grey | `circle-minus` | `#5C5853` / `#EFEDEA` | `#A39C94` / `#2A2622` |

Rules:

1. **Never colour alone.** A status always shows icon + word ("To do", "Applied", "Missed"). Each icon has a different shape, so the status survives colour blindness, greyscale printing and bright sunlight.
2. **Red means late, nothing else.** Delete buttons are outlined in neutral ink with a red icon. "Closing soon" countdowns stay amber (bold) and do not turn red; red appears only when a task is missed or a payment is overdue.
3. **Green means finished.** Never use green for "go" buttons; primary buttons are brand purple.
4. Tone foregrounds are dark enough to be text (amber text is a burnt amber, not yellow). The soft backgrounds are for chips and banners only, never for large panels.

### 2.4 Contrast (WCAG 2.1 AA)

Ratios computed from the WCAG relative-luminance formula, rounded down to 2 decimals. Text needs 4.5:1 (3:1 at ≥ 24 px, or ≥ 18.67 px bold). Non-text UI (focus ring, input borders, icons) needs 3:1. **Every pair below passes.**

**Light mode, text on surfaces**

| Foreground | on base `#FFFFFF` | on muted `#FAF8F5` | on sunken `#F2EFEA` |
| --- | --- | --- | --- |
| ink.900 `#1C1917` | 17.49 | 16.50 | 15.25 |
| ink.600 `#57534E` | 7.63 | 7.20 | 6.65 |
| ink.400 `#6F6863` | 5.47 | 5.16 | 4.77 |
| brand.600 `#5B2A86` | 9.90 | 9.34 | 8.64 |
| tone.amber `#A14F00` | 5.78 | 5.45 | 5.04 |
| tone.blue `#1D5FC2` | 6.05 | 5.71 | 5.28 |
| tone.green `#1A7034` | 6.16 | 5.81 | 5.37 |
| tone.red `#B42318` | 6.57 | 6.20 | 5.73 |
| tone.grey `#5C5853` | 7.06 | 6.66 | 6.15 |

**Light mode, pairs**

| Pair | Ratio |
| --- | --- |
| onBrand `#FFFFFF` on brand.600 (primary button) | 9.90 |
| brand.900 on brand.50 (selected) | 14.44 |
| brand.600 on brand.50 | 8.78 |
| ink.900 on brand.50 (admin hover row) | 15.50 |
| amber on amber-soft | 5.17 |
| blue on blue-soft | 5.22 |
| green on green-soft | 5.39 |
| red on red-soft | 5.58 |
| grey on grey-soft | 6.04 |
| brand.500 focus ring on base (non-text) | 7.57 |
| line.strong on base / muted (non-text) | 3.74 / 3.53 |
| gold on base (decorative; would pass non-text anyway) | 3.72 |

**Dark mode, text on surfaces**

| Foreground | on base `#1F1C19` | on muted `#141210` | on sunken `#2A2622` |
| --- | --- | --- | --- |
| ink.900 `#F5F2EE` | 15.19 | 16.75 | 13.45 |
| ink.600 `#C9C3BC` | 9.70 | 10.69 | 8.59 |
| ink.400 `#A39C94` | 6.25 | 6.89 | 5.53 |
| brand.600 `#C4A3E8` | 7.87 | 8.68 | 6.97 |
| tone.amber `#F2B755` | 9.43 | 10.39 | – |
| tone.blue `#8DB8F5` | 8.32 | 9.17 | – |
| tone.green `#7FCF95` | 9.09 | 10.02 | – |
| tone.red `#F59A90` | 8.00 | 8.82 | – |
| tone.grey `#A39C94` | 6.25 | 6.89 | 5.53 |

**Dark mode, pairs**

| Pair | Ratio |
| --- | --- |
| onBrand `#2E1245` on brand.600 (primary button) | 7.57 |
| brand.900 on brand.50 | 12.27 |
| brand.600 on brand.50 | 7.46 |
| ink.900 on brand.50 | 14.39 |
| amber / blue / green / red / grey on own soft | 7.69 / 7.48 / 7.40 / 7.44 / 5.53 |
| brand.500 focus ring on base (non-text) | 6.27 |
| line.strong on base / muted (non-text) | 3.63 / 4.00 |
| gold on base | 7.54 |

Follow-up for engineering: add a unit test in `packages/tokens` that recomputes these ratios from `colors` / `darkColors` and fails under the thresholds, so a future palette tweak cannot silently break AA.

### 2.5 Dark mode

- Default follows the system setting. Profile has "Appearance: System / Light / Dark".
- Client (React Native): `const palette = themes[useColorScheme() ?? "light"]`. Admin (web): CSS variables flip automatically under `prefers-color-scheme`, or forced with `<html data-theme="dark|light">`; no `dark:` classes needed for tokenised colours.
- Layers in dark mode are separated by surface lightness (muted < base < sunken) and `line.subtle`, not shadows.
- Images and illustrations get a dark variant or sit on `surface.base`; never invert photos.

---

## 3. Typography

### 3.1 Family: Noto Sans (+ Noto Sans Devanagari, Noto Sans Gujarati)

- One design family across Latin, Devanagari (Hindi) and Gujarati, with matched x-height and stroke, so a Gujarati name next to an English amount looks like one voice. Inter + Noto would mismatch metrics in mixed lines.
- Contains `₹` (U+20B9), tabular figures, and is open source (OFL). Free.
- Numbers use **tabular figures** (`fontVariant: ["tabular-nums"]` in RN, `font-variant-numeric: tabular-nums` on web) so amounts line up in columns and countdowns do not jitter.
- Weights shipped: 400 Regular, 500 Medium, 600 SemiBold, 700 Bold.
- Size budget: ship Latin + `₹` subset for the 4 weights in the APK (about 4 × 150 KB). Devanagari and Gujarati files load only when the user picks Hindi or Gujarati (on web via `unicode-range`; on Android bundled later as part of the i18n feature, size note in that plan). Fallback is the system font, which on Android is already Noto.
- Dependency note: loading uses `expo-font` (part of Expo). Whether to add `@expo-google-fonts/noto-sans` or vendor the `.ttf` files in `apps/client/assets/fonts` is an implementation decision for the plan (vendoring adds no runtime dependency).

### 3.2 Scale

Sizes are at 100% system text size. On the client every style scales with dynamic type (Android font scale, iOS Text Size via the browser for the PWA). Do not set `allowFontScaling={false}`. Only the `amount` style caps growth at `maxFontSizeMultiplier = 1.6` (`amountMaxFontScale`) so a ₹1,48,500 total does not break mid-number; everything else scales freely and layouts must wrap.

| Token | Size / line | Weight | Use |
| --- | --- | --- | --- |
| amount | 40 / 48 | 700 | The one hero number on a card: "To pay ₹86,580" |
| display | 32 / 40 | 700 | Screen-level totals, greeting on Home |
| title | 24 / 32 | 600 | Screen titles |
| heading | 20 / 28 | 600 | Card titles ("Apply for Jio Financial") |
| bodyLg | 18 / 26 | 400 | **Default client body text.** Our audience reads better at 18. |
| body | 16 / 24 | 400 | Secondary client text; default admin text |
| label | 16 / 22 | 600 | Buttons, chips, tab labels |
| bodySm | 14 / 20 | 400 | Admin tables; never for client sentences |
| caption | 13 / 18 | 500 | Timestamps ("Updated 2:15 pm"), helper text. Minimum size anywhere. |

- Hindi and Gujarati: multiply line height by `indicLineHeightScale` (1.2) so matras and conjuncts are not clipped. Allow 30–40% text expansion in every layout.
- Sentence case everywhere ("Mark applied", not "Mark Applied"). No all-caps paragraphs; caps only for the two IPO flags "APPLY" / "DO NOT APPLY" as badges, with letter-spacing 0.5.
- Never use weight alone to show status.

---

## 4. Space, radius, elevation, touch

- **Space** (4 px base): `0, 4, 8, 12, 16, 20, 24, 32, 40, 48, 64` as `space[0…16]`. Screen side padding 16 (20 on phones ≥ 400 dp). Gap between cards 12. Inside a card 16. Admin page padding 24–32.
- **Radius**: `sm 6` chips and inputs on admin, `md 10` buttons and inputs, `lg 16` cards, `xl 24` bottom sheets and the Do-now card, `full` pills and avatars.
- **Elevation**: `0` flat; `1` resting card (1 px `line.subtle` border + tiny shadow); `2` sticky bottom bar, dropdowns; `3` modal and bottom sheet. Dark mode: borders and lighter surfaces instead of shadows.
- **Touch targets**: minimum 44 × 44 (constitution); client uses **48 dp** for every control (`touchTarget.comfortable`). Primary buttons are full-width, 52 tall. At least 8 px between adjacent targets.

---

## 5. Iconography

- **Lucide** (outline, 24 px grid, stroke 1.75; 20 px in admin tables). Same icon names in both apps (`lucide-react` for admin, `lucide-react-native` + `react-native-svg` for client: both new dependencies, to be justified in the first UI plan).
- Icons support words; they never replace them. The only icon-only buttons are Copy, Close and the inbox bell, and each has an accessibility label.
- Fixed meanings: `copy` (copy UPI ID), `bell` (inbox), `indian-rupee` (payments), `list-checks` (tasks), `house` (home), `trending-up` (IPOs), `users` (family), plus the five status icons in 2.3.
- Festival motif (diya, rangoli, kite, etc.) are simple two-colour SVGs in `gold` + `brand.600`, supplied per festival by the operator's banner schedule.

---

## 6. Motion

Calm, quick, purposeful. All values are in `motion` and map directly to Reanimated (`withTiming(v, { duration, easing: Easing.bezier(...motion.easing.standard) })`, `withSpring(v, motion.spring)`) and to CSS variables.

| Use | Duration | Easing |
| --- | --- | --- |
| Press feedback (opacity 1 → 0.85, scale 0.98) | 120 | standard |
| Chip/status change cross-fade | 200 | standard |
| Card enter, sheet open | 320 | enter / spring |
| Sheet close, toast leave | 200 | exit |
| "Marked applied" tick draw | 320 | standard, then the row settles into place with `spring` |

- No looping animation, parallax, confetti or shimmer sweeps. Loading skeletons pulse opacity 0.6 ↔ 1 at 1.2 s, and stop with reduce motion.
- Reduce motion (OS setting): durations become 0; state changes still happen instantly.
- Countdowns update once a minute (once a second only in the final 60 s), without animating digits.
- Haptics (Android only): light tick on "Mark applied" and "Copied"; none on iPhone PWA (not supported).

---

## 7. Numbers, money, dates

| Topic | Rule | Example |
| --- | --- | --- |
| Currency | Always via `formatRupees` from `@divya/domain`; Indian grouping; `₹` with no space | ₹1,48,500 · ₹86,580 · ₹500 |
| Paise | Hidden. Amounts are whole rupees (BR-SET-07 rounds to the rupee). Show paise (`showPaise: true`) only in the admin ledger/audit views or if the operator changes rounding to paise. | ₹17,460 |
| Direction | Words, not signs. A balance is "You pay ₹X" or "We pay you ₹X", never "−₹X". | "We pay you ₹1,500" |
| Deductions in a breakdown | Minus sign U+2212 with a label, aligned right, tabular | "− Your share  ₹900" |
| Estimates | Prefix "About" and a blue "Estimate" chip until the listing price is in (BR-SET-09) | "About ₹17,100 · Estimate" |
| Abbreviation | Never abbreviate an amount someone must pay. Admin charts may use "₹1.5 L", "₹2.3 Cr" on axes only. | |
| Shares and lots | "1 lot (14 shares)", "Sell 14 shares" | |
| Percent | No decimals unless needed | 30% |
| Dates | Day + date + short month; year only if not this year | Fri, 14 Nov · 3 Apr 2027 |
| Times | 12-hour, lower-case am/pm, IST implied on the client, "IST" shown on admin | 10:00 am – 5:00 pm |
| Relative time | Under 24 h use relative, else absolute | "Closes in 2 h 15 min", "Due Tue, 18 Nov, 11:59 pm" |
| Financial year | Apr–Mar, labelled "FY 2026–27" and quarters "Q3 (Oct–Dec)" | |
| PAN | Always masked as in the constitution | ABCDE••••F |
| UPI ID | Monospace not required; shown in full with a Copy button | divya@okhdfc |

Large numbers: the hero amount of a card uses `amount` (40) and is the visually heaviest element; supporting amounts use `heading` or `bodyLg` with tabular figures and right alignment.

---

## 8. State patterns (every screen)

Constitution VII requires loading, empty, error and offline on every screen.

| State | Pattern | Copy shape |
| --- | --- | --- |
| Loading (first load) | Skeleton blocks in the exact shape of the content (cards, chips, rows) on `surface.sunken`. No full-screen spinner. Content keeps its place; nothing jumps. | Screen reader: "Loading tasks" |
| Loading (refresh) | Pull to refresh on client; thin progress bar under the header on admin. Old data stays visible. | |
| Empty | Small line illustration (gold + brand), one sentence on what this is and why it's empty, one action if there is one. Empty is good news here, say so. | "No tasks right now. We'll remind you when an IPO opens." |
| Error | Inline card in the content area, `tone.red` icon, plain message (what happened + what to do), **Try again** button. Never a raw code; a short reference ID in caption for support. | "We couldn't load your payments. Check your internet and try again." |
| Offline | Persistent slim banner under the header on `surface.sunken` with a `wifi-off` icon. Cached data stays visible with "Last updated 2:15 pm". Actions that change money or tasks (Mark applied, Submit deal, Payment done) are disabled with a reason, or queued if the feature spec allows (offline queueing is a spec decision, not assumed here). | "You're offline. Showing what we saved at 2:15 pm." |
| Success | Inline confirmation in place (row turns green, chip changes) plus a short toast with Undo where the rules allow undo. No modal for success. | "Marked applied for Rajesh." |
| Pending approval | Blue "Waiting" chip with the exact thing being waited for. | "Waiting for approval" |

Toasts: bottom, above the tab bar, 4 s, `role="status"`/`accessibilityLiveRegion="polite"`; errors stay until dismissed.

---

## 9. Platform notes (Android app vs iPhone PWA)

| Topic | Android (Expo app) | iPhone (PWA on Home Screen) |
| --- | --- | --- |
| Navigation | Bottom tabs: Home, Tasks, IPOs, Payments, More. System back button closes sheets first. | Same tabs; no system back, so every pushed screen has a visible back chevron with label. Respect safe-area insets (notch and home indicator). |
| Copy UPI ID | `expo-clipboard`, haptic tick, toast "Copied" | `navigator.clipboard`; works only after a tap (it is). Toast "Copied". |
| Open UPI app | `upi://pay?...` intent opens the chooser | `upi://` links open installed UPI apps on iOS inconsistently: show **Pay via UPI** plus visible UPI ID + amount with Copy buttons as the reliable fallback. |
| Push | FCM channels "Tasks (urgent)", "Updates", "Greetings". One-time autostart guide on Xiaomi/Vivo/Oppo/Realme. | Web Push only after Add to Home Screen; Inbox and email fallback cover misses. Show an "Add to Home Screen" guide if opened in Safari. |
| Biometric unlock | Fingerprint/face via system prompt | WebAuthn where available, else PIN |
| Dynamic type | System font scale | Safari text size; use rem units in web styles |
| Haptics | Yes | No |

---

## 10. Accessibility checklist (applies to every spec)

- Contrast per section 2.4; never place text on `gold` or on photos.
- Targets ≥ 48 dp client, ≥ 44 px admin, 8 px apart.
- Every control has a name, role and state (`accessibilityRole`, `accessibilityState`, `aria-*`). Status chips read as "Status: To do".
- Amounts are read in full: set `accessibilityLabel="Rupees 86,580"` style labels via one helper so screen readers do not say "R S".
- Countdowns are not live regions (they would chatter); the label carries the absolute time: "Window closes at 5:00 pm, in 2 hours 15 minutes".
- Focus order follows reading order; modals and sheets trap focus and return it on close; admin tables are fully keyboard operable (arrow keys in grid, Space to select, Shift+Space range).
- 200% text: no truncation of amounts, names or buttons; rows wrap instead.
- Language: set `lang` per screen locale so screen readers pick the right voice for Hindi/Gujarati.
