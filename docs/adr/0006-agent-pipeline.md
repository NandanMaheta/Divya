# ADR 0006: Spec Kit + Claude Code agent pipeline

- Status: Accepted (2026-11-13)

## Context

A solo owner wants agents to build the product with minimal supervision and high quality.

## Decision

Spec Kit 1.4 (pinned) drives specify → plan → tasks → implement, plus the `bug` and `assess` extensions. Claude Code project agents (`.claude/agents`) own each role; hooks enforce formatting, path guards and "done" checks; CI is the final gate. Human gates: spec approval, owner approval on money/auth PRs, UAT, production release. See `docs/product/automation-blueprint.md`.

## Consequences

Repeatable, auditable delivery. Process weight is managed by using the bug extension and plan mode for small changes.
