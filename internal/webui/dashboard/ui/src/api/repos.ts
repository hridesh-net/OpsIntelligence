import { api } from "./client";

export interface Repo {
  id: string;
  full_name?: string;
  platform?: string;
  index_status?: string;
  scan_status?: string;
  risk_level?: string;
  user_count?: number;
  language?: string;
  default_branch?: string;
  description?: string;
}

export interface ScanStatus {
  status?: string;
  last_scan_at?: string;
  total_findings?: number;
  critical?: number;
  high?: number;
  medium?: number;
  low?: number;
}

export async function listRepos(): Promise<Repo[]> {
  const res = await api<{ repos?: Repo[] }>("/api/v1/repos");
  return res.repos ?? [];
}

export async function getRepo(id: string): Promise<Repo> {
  return api<Repo>(`/api/v1/repos/${encodeURIComponent(id)}`);
}

export async function getScan(id: string): Promise<ScanStatus | null> {
  return api<ScanStatus>(`/api/v1/repos/${encodeURIComponent(id)}/scan`).catch(() => null);
}

export async function syncRepo(id: string): Promise<void> {
  await api(`/api/v1/repos/${encodeURIComponent(id)}/sync`, { method: "POST", body: {} });
}

// ── Repo intelligence (LLM-extracted memory) ────────────────────────────────

export interface Dependency { name: string; version?: string; purpose?: string }
export interface CodeConvention { name: string; pattern: string }

export interface RepoMemory {
  repo_id: string;
  updated_at?: string;
  head_sha?: string;
  architecture?: string;
  primary_lang?: string;
  languages?: string[];
  key_files?: string[];
  conventions?: CodeConvention[];
  dependencies?: Dependency[];
  test_patterns?: string;
  ci_summary?: string;
  review_hints?: string;
  common_issues?: string[];
  user_context?: string;
}

export async function getRepoMemory(id: string): Promise<RepoMemory | null> {
  return api<RepoMemory>(`/api/v1/repos/${encodeURIComponent(id)}/memory`).catch(() => null);
}

// ── Code call graph ─────────────────────────────────────────────────────────

export interface CallNode {
  id: string;
  name: string;
  file: string;
  line: number;
  kind: string; // function | method | class | module | file
  package?: string;
}
export interface CallEdge { from: string; to: string; kind: string } // call | import
export interface CallGraph {
  repo_id: string;
  created_at?: string;
  nodes: CallNode[];
  edges: CallEdge[];
}

export async function getCallGraph(id: string): Promise<CallGraph | null> {
  return api<CallGraph>(`/api/v1/repos/${encodeURIComponent(id)}/callgraph`).catch(() => null);
}
