---

name: rn-screen-pattern
description: How to build a Divya client app screen in Expo Router that works on Android and the iPhone PWA, with data hooks, states, tests and a Maestro flow. Use for any screen under apps/client.
---

# Client screen pattern

```
apps/client/src/app/(tabs)/<screen>.tsx      // route: thin, composes features
apps/client/src/features/<feature>/
  use-<thing>.ts          // TanStack Query hook over @divya/api-client (query keys in keys.ts)
  <thing>-card.tsx        // presentational components (tokens only, no inline colours)
  <thing>.test.tsx        // component tests for loading / empty / error / data
apps/client/.maestro/<feature>.yaml          // journey flow
```

Rules: urgent action reachable in ≤2 taps from Home; amounts via `formatRupees`; dates in IST with Indian formats; every list uses FlashList; images via expo-image; haptics on primary actions (native only); check the web export renders the same screen. Notification taps deep-link to the screen (`divya://...`).
