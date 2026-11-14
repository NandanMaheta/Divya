# Brief: <NNN> <feature name>  ·  Tier <A|B>

<!-- One page maximum. Agents read this first; link to spec/plan/contracts sections instead of copying them. -->

**Goal (2–3 lines):** what the feature delivers and for whom.

**Business rules:** BR-… (link to `docs/product/business-rules.md`), golden scenarios affected: GS-…

**Acceptance criteria (one line each, IDs from spec.md):**
- AC-1 …
- AC-2 …

**Public surface:**
- Go: package `internal/<module>`: `Type`, `Func(...)` (see `contracts/go-module-api.md`)
- HTTP: `METHOD /v1/...` (see `contracts/*.openapi.yaml`)
- DB: migration `NNNNN_<name>.sql`: tables …

**Files to touch:** `apps/api/internal/<module>/…`, `apps/api/queries/<module>.sql`, …

**Depends on:** features / packages (see `docs/engineering/codemap.md`)

**Risks and non-goals:** …
