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
