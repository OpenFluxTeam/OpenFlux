# Contributing to OpenFlux — read this first

The project **wiki is the source of truth** for how OpenFlux is built and how we want
it built, across all of its repos — not only the code in this one:

→ **https://github.com/OpenFluxTeam/OpenFlux/wiki**

Before changing anything here — by hand or with an AI assistant — read, in order:

1. **Start-Here** — the map, and the contract.
2. **Source-of-Truth** — which repo and branch owns each piece; edit the canonical copy.
3. **Conventions** — names, versions, signing, branches, the hard rules.
4. **Status-and-Roadmap** — what already exists or is in progress; don't redo it.
5. **Decisions** — why things are the way they are; don't quietly undo them.

The lines that trip people up most:

- When the wiki and your reading of the code disagree, **follow the wiki** and report the
  mismatch. The code shows *what is*; the wiki says *what is canonical, and why*.
- **Don't re-derive or fork work that already exists** — extend it.
- JS/script transports live in the standalone **OpenFluxTransports** repo, not in the core
  (`ADR-002`); never rebuild a shipped, signed transport from an older or loose copy.
- A carrier never sends the project's name to a third-party service.
- **Nothing is pushed, merged, or released without the maintainer's okay.**

This file exists so that an editor — or an AI assistant — opening the repo is sent to the
wiki first, instead of re-deriving the project from the code.
