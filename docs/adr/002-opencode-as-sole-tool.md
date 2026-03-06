# ADR 002 — opencode as the sole supported tool

**Status:** Accepted

## Context

`construct` launched with support for multiple AI coding tools. Each tool has
its own authentication model, configuration surface, runtime behaviour, and
failure modes. Supporting a fan of tools means users arrive with different API
keys, different auth flows, and different bugs — and we have to be able to
reproduce and fix all of them.

`opencode` is where we want to invest. There is meaningful work ahead to make
the `construct` + `opencode` experience exactly what it should be, and that
work requires focus. Spreading effort across tools we do not use ourselves
dilutes that focus without providing any benefit to the users we actually serve.

## Decision

`opencode` is the only tool `construct` supports. No other tool integrations
will be added or maintained.

## Consequences

- All effort goes into making the `construct` + `opencode` experience excellent.
- Users need only understand one set of credentials (`ANTHROPIC_API_KEY` /
  `OPENAI_API_KEY`). Support surface is contained and predictable.
- The codebase contains no dormant tool code that has to be reasoned about,
  kept compiling, or explained to contributors.
