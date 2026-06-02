import { api } from "./client";
import type { Principal } from "./types";

export interface AuthStatus {
  bootstrap_needed: boolean;
  min_password_length: number;
}

export async function getAuthStatus(): Promise<AuthStatus> {
  return api<AuthStatus>("/api/v1/auth/status");
}

export async function whoami(): Promise<Principal | null> {
  try {
    return await api<Principal>("/api/v1/whoami");
  } catch {
    return null;
  }
}

export async function login(username: string, password: string): Promise<void> {
  await api("/api/v1/auth/login", { method: "POST", body: { username, password } });
}

export async function bootstrap(username: string, email: string, password: string): Promise<void> {
  await api("/api/v1/auth/bootstrap", { method: "POST", body: { username, email, password } });
}

export async function logout(): Promise<void> {
  await api("/api/v1/auth/logout", { method: "POST", body: {} }).catch(() => undefined);
}
