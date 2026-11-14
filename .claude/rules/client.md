---

paths:

- "apps/client/**"

---

# Client app rules

- Read `apps/client/AGENTS.md` (Expo's own guidance) first; check versioned Expo docs, never memory.
- Expo Router routes live in `src/app/`; components/hooks outside it. Install packages with `npx expo install`.
- One codebase for Android and the iPhone PWA: test both (`expo start --web`). Avoid native-only APIs without a web fallback.
- Screens handle loading/empty/error/offline; urgent actions reachable in ≤2 taps from Home; Indian formatting via `@swastik/domain`; colours/spacing only from `@swastik/tokens`.
- Each screen gets a Maestro flow in `apps/client/.maestro/` and component tests.
- Keep Android download < 25 MB: no heavy dependency without a size note in the plan.
