/* ============================================================
   SCRUN — Zustand store (single source of truth)
   Immer middleware lets actions mutate a draft just like the
   original imperative code; React re-renders from the result.
   ============================================================ */
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";

import type { Agent, AgentKey, Card, Filters, Layout, Density, PanelTab, Screen, Theme } from "../types";
import { AGENT_STATS, createAgents, createCards, createWorkflow } from "../data/seed";
import type { AppData } from "./state";
import * as L from "./logic";
import { applyBoardConfig, suStages, SU_WORKFLOWS } from "./setup";
import { loadPrefs, loadSetup, savePrefs, saveSetup } from "./persist";
import { autoKey } from "../lib/helpers";
import * as API from "../api/kanban";

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v));
const PALETTE = ["#586675", "#2898da", "#4db0ef", "#a78bfa", "#f5b042", "#34d399", "#f4685f", "#2dd4bf"];

function initialData(): AppData {
  const prefs = loadPrefs();
  const saved = loadSetup();
  const agents = createAgents();
  const firstFive = Object.keys(agents).slice(0, 5);

  const data: AppData = {
    agents,
    workflow: createWorkflow(),
    cards: createCards(),
    agentStats: AGENT_STATS,
    activity: [],
    seq: 380,
    prefix: "AI",

    boardId: null,
    boardName: "AI Workforce Board",
    boardKey: "AI",
    boardColor: "#e4572e",
    boardDesc: "",
    boardAgents: firstFive,

    boards: [],

    apiMode: "loading",

    phase: "setup",
    screen: "board",
    layout: prefs.layout ?? "columns",
    density: prefs.density ?? "rich",
    theme: prefs.theme ?? "light",
    accent: prefs.accent ?? "#e4572e",
    simSpeed: 2200,
    simRunning: prefs.simRunning ?? true,
    dragging: false,
    showRail: prefs.showRail ?? true,
    railCollapsed: prefs.railCollapsed ?? false,
    filters: { agent: "all", prio: "all", type: "all", q: "" },
    selectedId: null,
    panelTab: "details",
    tickN: 0,
    flashId: null,

    toast: { msg: "", n: 0 },

    taskDraft: null,
    taskIsNew: false,
    agentDraftKey: null,
    agentDraft: null,

    setup: {
      step: 0,
      name: "AI Workforce Board",
      key: "AI",
      desc: "",
      color: "#e4572e",
      preset: "dev",
      stages: null,
      agents: firstFive,
      done: false,
    },
  };

  if (saved && saved.done) {
    Object.assign(data.setup, {
      name: saved.name ?? data.setup.name,
      key: saved.key ?? data.setup.key,
      color: saved.color ?? data.setup.color,
      desc: saved.desc ?? "",
      preset: saved.preset ?? "dev",
      stages: saved.stages ?? null,
      agents: saved.agents ?? firstFive,
      done: true,
    });
    applyBoardConfig(data);
    L.seedActivity(data);
    data.accent = data.setup.color;
    data.phase = "app";
  }
  return data;
}

export interface Actions {
  /* navigation + ui */
  go: (screen: Screen) => void;
  setLayout: (l: Layout) => void;
  setDensity: (d: Density) => void;
  setTheme: (t: Theme) => void;
  toggleTheme: () => void;
  setAccent: (hex: string) => void;
  toggleSim: () => void;
  setSimSpeed: (ms: number) => void;
  toggleRail: () => void;
  toggleRailCollapsed: () => void;
  setFilter: (key: keyof Filters, val: string) => void;
  setSearch: (q: string) => void;
  clearFilters: () => void;
  showToast: (msg: string) => void;

  /* board / cards */
  setDragging: (b: boolean) => void;
  dropCard: (id: string, colId: string) => void;
  openPanel: (id: string) => void;
  closePanel: () => void;
  setPanelTab: (t: PanelTab) => void;
  panelAction: (act: string) => void;
  resolveCardHitl: (id: string, optIdx: number) => void;

  /* task form */
  openTaskForm: (id?: string | null, colId?: string) => void;
  closeTaskForm: () => void;
  updateTaskDraft: (patch: Partial<Card>) => void;
  saveTaskForm: () => void;
  deleteTask: (id: string) => void;

  /* agent config */
  openAgentConfig: (key: AgentKey) => void;
  closeAgentConfig: () => void;
  updateAgentDraft: (path: string, val: unknown) => void;
  saveAgentConfig: () => void;

  /* workflow builder */
  renameStage: (id: string, name: string) => void;
  toggleStageWip: (id: string) => void;
  toggleStageGate: (id: string) => void;
  cycleStageDye: (id: string) => void;
  deleteStage: (id: string) => void;
  addStage: () => void;
  reorderStages: (fromId: string, toId: string) => void;
  applyWorkflowPreset: (p: string) => void;

  /* setup wizard */
  suSetStep: (n: number) => void;
  suPatch: (patch: Partial<AppData["setup"]>) => void;
  suToggleAgent: (k: AgentKey) => void;
  suSelectAllAgents: () => void;
  suClearAgents: () => void;
  suRenameStage: (i: number, name: string) => void;
  suToggleGate: (i: number) => void;
  suRemoveStage: (i: number) => void;
  suAddStage: () => void;
  suReorderStages: (from: number, to: number) => void;
  suNext: () => void;
  suBack: () => void;
  startSetup: () => void;
  reconfigureBoard: () => void;

  /* sim */
  tick: () => void;

  /* live api */
  hydrateFromApi: () => Promise<void>;
  switchBoard: (id: string) => Promise<void>;
  /** Open one board's full workspace (remembers it for deep links). */
  openBoard: (id: string) => Promise<void>;
  /** Return to the boards gallery and refresh the list. */
  goToBoards: () => Promise<void>;
}

export type Store = AppData & Actions;

export const useStore = create<Store>()(
  immer((set, get) => {
    const persist = () => savePrefs(get());
    const scheduleFlashClear = () =>
      setTimeout(() => set((s) => void (s.flashId = null)), 820);

    function ensureSuStages(s: AppData) {
      if (!s.setup.stages)
        s.setup.stages = suStages(s.setup).map((st) => [st.id, st.name, st.dot, st.gate] as [string, string, string, typeof st.gate]);
      return s.setup.stages;
    }

    return {
      ...initialData(),

      /* ---------- navigation + ui ---------- */
      go: (screen) => {
        set((s) => void (s.screen = screen));
      },
      setLayout: (l) => {
        set((s) => void (s.layout = l));
        persist();
      },
      setDensity: (d) => {
        set((s) => void (s.density = d));
        persist();
      },
      setTheme: (t) => {
        set((s) => void (s.theme = t));
        persist();
      },
      toggleTheme: () => {
        set((s) => void (s.theme = s.theme === "dark" ? "light" : "dark"));
        persist();
      },
      setAccent: (hex) => {
        set((s) => void (s.accent = hex));
        persist();
      },
      toggleSim: () => {
        set((s) => void (s.simRunning = !s.simRunning));
        persist();
      },
      setSimSpeed: (ms) => {
        set((s) => void (s.simSpeed = ms));
        persist();
      },
      toggleRail: () => {
        set((s) => void (s.showRail = !s.showRail));
        persist();
      },
      toggleRailCollapsed: () => {
        set((s) => void (s.railCollapsed = !s.railCollapsed));
        persist();
      },
      setFilter: (key, val) => set((s) => void (s.filters[key] = val)),
      setSearch: (q) => set((s) => void (s.filters.q = q)),
      clearFilters: () =>
        set((s) => {
          s.filters = { agent: "all", prio: "all", type: "all", q: "" };
        }),
      showToast: (msg) => set((s) => void (s.toast = { msg, n: s.toast.n + 1 })),

      /* ---------- board / cards ---------- */
      setDragging: (b) => set((s) => void (s.dragging = b)),
      dropCard: (id, colId) => {
        const prev = get().cards.find((c) => c.id === id)?.col;
        set((s) => L.moveCard(s, id, colId, true));
        scheduleFlashClear();
        const boardId = get().boardId;
        if (boardId && get().apiMode === "live" && prev !== colId) {
          API.moveCard(boardId, id, colId).catch((e) => {
            set((s) => {
              const card = s.cards.find((c) => c.id === id);
              if (card && prev) card.col = prev;
            });
            get().showToast(`Move failed: ${(e as Error).message}`);
          });
        }
      },
      openPanel: (id) => set((s) => {
        s.selectedId = id;
        s.panelTab = "details";
      }),
      closePanel: () => set((s) => void (s.selectedId = null)),
      setPanelTab: (t) => set((s) => void (s.panelTab = t)),
      panelAction: (act) => {
        let toast: string | null = null;
        set((s) => {
          const k = s.cards.find((x) => x.id === s.selectedId);
          if (!k) return;
          if (act === "approve") {
            L.resolveHitl(s, k, 0, false);
          } else if (act === "reject") {
            L.resolveHitl(s, k, k.hitl ? k.hitl.opts.length - 1 : 1, true);
          } else {
            toast = L.panelAction(s, k, act);
          }
        });
        if (act === "approve") toast = "Decision approved";
        if (act === "reject") toast = "Sent back for rework";
        if (toast) get().showToast(toast);
        scheduleFlashClear();
      },
      resolveCardHitl: (id, optIdx) => {
        set((s) => {
          const k = s.cards.find((x) => x.id === id);
          if (k) L.resolveHitl(s, k, optIdx, false);
        });
        get().showToast("Decision approved");
        scheduleFlashClear();
      },

      /* ---------- task form ---------- */
      openTaskForm: (id, colId) =>
        set((s) => {
          if (id) {
            const k = s.cards.find((x) => x.id === id);
            if (!k) return;
            s.taskDraft = clone(k);
            s.taskIsNew = false;
          } else {
            s.taskDraft = {
              id: L.nextId(s),
              col: colId || "backlog",
              type: "feat",
              prio: "M",
              title: "",
              agents: [],
              status: "queued",
              labels: [],
              desc: "",
              ac: [],
              when: "now",
              logs: [],
              progress: 0,
            };
            s.taskIsNew = true;
          }
        }),
      closeTaskForm: () => set((s) => void (s.taskDraft = null)),
      updateTaskDraft: (patch) =>
        set((s) => {
          if (s.taskDraft) Object.assign(s.taskDraft, patch);
        }),
      saveTaskForm: () => {
        const st = get();
        if (!st.taskDraft) return;
        if (!st.taskDraft.title.trim()) {
          get().showToast("Give the ticket a title");
          return;
        }
        const isNew = st.taskIsNew;
        const draftId = st.taskDraft.id;
        set((s) => {
          const d = s.taskDraft!;
          if (isNew) {
            if (!d.agents.length) d.agents = ["devops"];
            s.cards.push(clone(d));
            const created = s.cards[s.cards.length - 1];
            L.logActivity(s, created, "move", `created in <b>${L.stageOf(s, created.col)?.name}</b>`);
            s.screen = "board";
          } else {
            const k = s.cards.find((x) => x.id === d.id);
            if (k) Object.assign(k, clone(d));
          }
          s.taskDraft = null;
        });
        if (isNew) {
          get().openPanel(draftId);
          get().showToast("Ticket #" + draftId + " created");
        } else {
          get().showToast("Ticket updated");
        }
      },
      deleteTask: (id) => {
        set((s) => {
          const i = s.cards.findIndex((x) => x.id === id);
          if (i >= 0) s.cards.splice(i, 1);
          s.taskDraft = null;
          if (s.selectedId === id) s.selectedId = null;
        });
        get().showToast("Ticket deleted");
      },

      /* ---------- agent config ---------- */
      openAgentConfig: (key) =>
        set((s) => {
          s.agentDraftKey = key;
          s.agentDraft = clone(s.agents[key]);
        }),
      closeAgentConfig: () =>
        set((s) => {
          s.agentDraftKey = null;
          s.agentDraft = null;
        }),
      updateAgentDraft: (path, val) =>
        set((s) => {
          if (!s.agentDraft) return;
          const parts = path.split(".");
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          let o: any = s.agentDraft;
          for (let i = 0; i < parts.length - 1; i++) o = o[parts[i]];
          o[parts[parts.length - 1]] = val;
        }),
      saveAgentConfig: () => {
        const key = get().agentDraftKey;
        const draft = get().agentDraft;
        if (!key || !draft) return;
        set((s) => {
          Object.assign(s.agents[key], clone(draft));
          s.agentDraftKey = null;
          s.agentDraft = null;
        });
        get().showToast(draft.name + " configuration saved");
      },

      /* ---------- workflow builder ---------- */
      renameStage: (id, name) =>
        set((s) => {
          const st = L.stageOf(s, id);
          if (st) st.name = name.trim() || st.name;
        }),
      toggleStageWip: (id) =>
        set((s) => {
          const st = L.stageOf(s, id);
          if (st) st.wip = st.wip ? 0 : 4;
        }),
      toggleStageGate: (id) =>
        set((s) => {
          const st = L.stageOf(s, id);
          if (st) st.gate = st.gate === "human" ? "auto" : st.gate === "auto" ? null : "human";
        }),
      cycleStageDye: (id) =>
        set((s) => {
          const st = L.stageOf(s, id);
          if (st) st.dot = PALETTE[(PALETTE.indexOf(st.dot) + 1) % PALETTE.length];
        }),
      deleteStage: (id) => {
        if (get().workflow.length <= 2) {
          get().showToast("Keep at least two stages");
          return;
        }
        set((s) => {
          const idx = s.workflow.findIndex((st) => st.id === id);
          const fallback = s.workflow[idx - 1] || s.workflow[idx + 1];
          s.cards.forEach((k) => {
            if (k.col === id) k.col = fallback.id;
          });
          s.workflow.splice(idx, 1);
        });
        get().showToast("Stage removed");
      },
      addStage: () => {
        set((s) => {
          const id = "stage" + Date.now();
          s.workflow.splice(s.workflow.length - 1, 0, {
            id,
            name: "New Stage",
            dot: "#2898da",
            wip: 0,
            gate: null,
            rules: { autoAssign: null, autoValidate: false },
          });
        });
        get().showToast("Stage added");
      },
      reorderStages: (fromId, toId) =>
        set((s) => {
          const from = s.workflow.findIndex((st) => st.id === fromId);
          const to = s.workflow.findIndex((st) => st.id === toId);
          if (from < 0 || to < 0 || from === to) return;
          const [m] = s.workflow.splice(from, 1);
          s.workflow.splice(to, 0, m);
        }),
      applyWorkflowPreset: (p) => {
        set((s) => L.applyWorkflowPreset(s, p));
        get().showToast("Preset applied");
      },

      /* ---------- setup wizard ---------- */
      suSetStep: (n) => set((s) => void (s.setup.step = n)),
      suPatch: (patch) => set((s) => void Object.assign(s.setup, patch)),
      suToggleAgent: (k) =>
        set((s) => {
          const i = s.setup.agents.indexOf(k);
          if (i >= 0) s.setup.agents.splice(i, 1);
          else s.setup.agents.push(k);
        }),
      suSelectAllAgents: () => set((s) => void (s.setup.agents = Object.keys(s.agents))),
      suClearAgents: () => set((s) => void (s.setup.agents = [])),
      suRenameStage: (i, name) =>
        set((s) => {
          const a = ensureSuStages(s);
          a[i][1] = name.trim() || a[i][1];
        }),
      suToggleGate: (i) =>
        set((s) => {
          const a = ensureSuStages(s);
          const g = a[i][3];
          a[i][3] = g === "human" ? "auto" : g === "auto" ? null : "human";
        }),
      suRemoveStage: (i) => {
        const a = get().setup.stages || suStages(get().setup).map((st) => [st.id, st.name, st.dot, st.gate]);
        if (a.length <= 2) {
          get().showToast("Keep at least two stages");
          return;
        }
        set((s) => {
          ensureSuStages(s).splice(i, 1);
        });
      },
      suAddStage: () =>
        set((s) => {
          const a = ensureSuStages(s);
          a.splice(a.length - 1, 0, ["s" + Date.now(), "New Stage", "#2898da", null]);
        }),
      suReorderStages: (from, to) =>
        set((s) => {
          const a = ensureSuStages(s);
          const [m] = a.splice(from, 1);
          a.splice(to, 0, m);
        }),
      suNext: () => {
        const s0 = get();
        if (s0.setup.step === 0 && !s0.setup.name.trim()) {
          get().showToast("Name your board");
          return;
        }
        if (s0.setup.step < 3) {
          set((s) => void (s.setup.step += 1));
        } else {
          // Launch: flip the local view to the board immediately so the
          // design's "wizard fades out → board appears" beat is preserved
          // even when the backend is slow or offline.
          set((s) => {
            applyBoardConfig(s);
            L.seedActivity(s);
            s.setup.done = true;
            s.accent = s.setup.color;
            s.phase = "app";
            s.screen = "board";
          });
          saveSetup(get().setup);
          // Live API: persist the new board server-side. Build the payload
          // in the shape gateway/kanban_api.go createBoardRequest expects:
          // agents as createAgentRequest objects, columns with position,
          // and color/desc/key tucked into config (no top-level fields).
          const st = get();
          if (st.apiMode === "live") {
            const stages = st.setup.stages
              ?? (st.workflow.map((s) => [s.id, s.name, s.dot, s.gate] as [string, string, string, typeof s.gate]));
            const payload = {
              name: st.setup.name,
              preset: st.setup.preset,
              config: {
                key: st.setup.key,
                color: st.setup.color,
                description: st.setup.desc,
              },
              columns: stages.map(([_id, name, color, gate], position) => ({
                name,
                position,
                color,
                gate: gate ?? "none",
              })),
              agents: st.setup.agents.map((key) => {
                const a = st.agents[key];
                if (!a) return null;
                return {
                  name: a.name,
                  agent_type: a.model || "claude-opus-4.7",
                  provider_id: (a.provider || "").toLowerCase(),
                  config: {
                    color: a.color,
                    ini: a.ini,
                    caps: a.caps,
                    role: a.role,
                    instructions: a.instructions,
                    knowledge: a.knowledge,
                    memory: a.memory,
                    autonomy: a.autonomy,
                    spend_cap_daily: a.spendCap,
                    max_parallel: a.maxParallel,
                  },
                };
              }).filter(Boolean),
            };
            get().showToast(get().setup.name + " is live");
            API.createBoard(payload as unknown as Record<string, unknown>)
              .then((b) => {
                set((s) => { s.boardId = b.id; });
                // Re-hydrate the specific board so it reflects what the
                // server actually stored (column IDs, agent IDs, normalised
                // values) — and land inside it, not back on the gallery.
                get().openBoard(b.id);
              })
              .catch((e) => {
                get().showToast(`Save failed: ${(e as Error).message}`);
              });
          } else {
            get().showToast(get().setup.name + " is live");
          }
        }
      },
      suBack: () => set((s) => void (s.setup.step = Math.max(0, s.setup.step - 1))),
      startSetup: () => set((s) => {
        s.phase = "setup";
        s.setup.step = 0;
      }),
      reconfigureBoard: () => {
        const saved = loadSetup();
        set((s) => {
          if (saved) {
            Object.assign(s.setup, {
              name: saved.name ?? s.setup.name,
              key: saved.key ?? s.setup.key,
              color: saved.color ?? s.setup.color,
              desc: saved.desc ?? "",
              preset: saved.preset ?? "dev",
              stages: saved.stages ?? null,
              agents: saved.agents ?? s.setup.agents,
            });
          }
          s.phase = "setup";
          s.setup.step = 0;
        });
      },

      /* ---------- sim ---------- */
      tick: () => set((s) => L.tick(s)),

      /* ---------- live api ---------- */
      hydrateFromApi: async () => {
        // Entry flow: land on the boards gallery — every configured board
        // plus "create new". Opening or creating a board enters the full
        // workspace. Transient API errors drop to "demo" so writeback paths
        // short-circuit; never bounce the user back to the wizard.
        try {
          const boards = await API.listBoards();
          set((s) => {
            s.apiMode = "live";
            s.boards = boards;
            s.phase = "boards";
          });
        } catch (e) {
          console.warn("[scrun] boards list failed:", e);
          set((s) => {
            s.apiMode = "demo";
          });
        }
      },
      openBoard: async (id) => {
        API.rememberBoard(id);
        const result = await API.loadBoard(id);
        if (!result.ok) {
          get().showToast("Could not open board — is the daemon running?");
          return;
        }
        const d = result.data;
        set((s) => {
          s.boardId = d.boardId;
          s.boardName = d.boardName;
          s.boardKey = d.boardKey;
          s.boardColor = d.boardColor;
          s.boardDesc = d.boardDesc;
          s.accent = d.boardColor;
          s.agents = d.agents;
          s.workflow = d.workflow;
          s.cards = d.cards;
          s.agentStats = d.agentStats;
          s.boardAgents = Object.keys(d.agents);
          s.apiMode = "live";
          s.phase = "app";
          s.screen = "board";
          s.setup.done = true;
          s.setup.name = d.boardName;
          s.setup.key = d.boardKey;
          s.setup.color = d.boardColor;
          s.setup.desc = d.boardDesc;
          s.setup.agents = Object.keys(d.agents);
        });
      },
      goToBoards: async () => {
        set((s) => {
          s.phase = "boards";
        });
        try {
          const boards = await API.listBoards();
          set((s) => {
            s.boards = boards;
          });
        } catch {
          /* keep the stale list; gallery still renders */
        }
      },
      switchBoard: async (id) => {
        await get().openBoard(id);
      },
    };
  }),
);

/* tiny derived selectors used widely */
export { suStages, SU_WORKFLOWS, autoKey };
