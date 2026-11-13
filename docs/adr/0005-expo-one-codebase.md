# ADR 0005: One Expo codebase for the Android app and the iPhone PWA

- Status: Accepted (2026-11-13)

## Context

Most clients use Android; some use iPhone. A native iOS app needs a paid Apple account and TestFlight renewals.

## Decision

`apps/client` is an Expo SDK 58 app with Expo Router. Android ships as a signed APK (OTA JS updates via EAS Update); iPhone users install the static web export as a PWA with Web Push (iOS 16.4+, Home Screen required). The admin panel is a separate Vite SPA.

## Consequences

One client codebase, ₹0 distribution. iPhone push is less reliable, so the in-app inbox is the source of truth and critical reminders also go by email.
