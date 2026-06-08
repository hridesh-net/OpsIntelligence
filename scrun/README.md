# Scrun — React + TypeScript

Autonomous **Agent Workforce Board** — a Kanban-style mission-control surface where a
fleet of AI agents pick up, work and ship tasks autonomously, with humans stepping in
only at approval gates. This is a full React + TypeScript + Vite port of the original
vanilla-JS prototype.

## Run it

```bash
npm install
npm run dev      # http://localhost:5173
```

```bash
npm run build    # type-check + production build into dist/
npm run preview  # serve the production build
```

> First run shows the **4-step setup wizard** (project → workflow → agents → review).
> Launch the board and the live simulation starts immediately. State persists to
> `localStorage`; clear it (key `scrun_setup`) to see the wizard again, or use the
> board menu → **Board setup**.

## Architecture

| Layer | Where |
|-------|-------|
| **State** | `src/store/` — a single [Zustand](https://github.com/pmndrs/zustand) store with the **Immer** middleware. `index.ts` holds state + actions; `logic.ts` is the pure domain logic (moves, HITL resolution, the live sim `tick`); `setup.ts`, `persist.ts` are helpers. The simulation just mutates the draft — React re-renders from the result, so there is no manual DOM patching. |
| **Types** | `src/types.ts`, `src/store/state.ts` |
| **Seed data** | `src/data/seed.ts` — agents, workflow, cards, stats |
| **Styling** | Global design tokens + primitives in `src/styles/` (`tokens.css`, `base.css`); every component ships a co-located `*.module.css` (CSS Modules). |
| **Components** | `src/components/` — `shell/` (rail, top bar, stats), `board/` (3 layouts + card + live rail), `modals/` (task detail, task form, agent config), `screens/` (workflows, agents, activity, analytics), `setup/` (wizard), `tweaks/` (live tweak panel). |

## Features

- **Board** in three layouts — columns, compact list, swimlanes-by-agent — with drag-and-drop between stages.
- **Live simulation** — agents advance work, stream terminal logs, spend budget, hit human-approval gates and merge to done. Pause/resume from the stats strip; tune tick speed in Tweaks.
- **Workflow builder** — reorder / rename stages, toggle WIP limits and approval / auto-validate gates, start from presets.
- **Agent manager + config modal** — role, capabilities, model, autonomy, spend cap, memory, knowledge sources.
- **Activity feed** and **Analytics** (throughput, work distribution donut, spend trend, cycle time, leaderboard).
- **Light / dark themes**, four brand accents, card density — all in the floating **Tweaks** panel (bottom-right).

## Tech

React 18 · TypeScript · Vite 5 · Zustand 4 + Immer. No other runtime dependencies.
